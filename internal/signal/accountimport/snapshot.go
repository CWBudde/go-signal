//go:build linux && (cgo || libsignal_go)

package accountimport

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cwbudde/go-signal/internal/signal"
)

type sourceFile struct {
	pathPin

	category string
	digest   [sha256.Size]byte
}

// snapshot owns all descriptors and the private copy. It does not open SQLite,
// create a destination or construct a live Signal client.
type snapshot struct {
	source                                              sourceTree
	root                                                *os.File
	databaseDir                                         *os.File
	sourcePath                                          string
	destination                                         destinationPath
	registry                                            sourceRegistry
	selected                                            registryEntry
	account                                             sourceAccount
	cache                                               []cacheEntry
	options                                             snapshotOptions
	workspace                                           string
	sqlPath                                             string
	tempPins                                            sourceTree
	tempParent                                          *os.Root
	tempRoot                                            *os.Root
	tempName                                            string
	closed                                              bool
	sourceRegions, destinationRegions, workspaceRegions []physicalRegion
}

func openSnapshot(ctx context.Context, opts signal.AccountImportOptions) (*snapshot, error) {
	return openSnapshotWithOptions(ctx, opts, snapshotOptions{limits: defaultSnapshotLimits()})
}

func openSnapshotWithOptions(ctx context.Context, opts signal.AccountImportOptions,
	config snapshotOptions,
) (_ *snapshot, err error) {
	err = ctx.Err()
	if err != nil {
		return nil, importError("snapshot canceled", err)
	}

	if opts.SourceDir == "" {
		return nil, importError("source directory", signal.ErrInvalidAccountImport)
	}

	snap := &snapshot{options: config}

	defer func() {
		if err != nil {
			err = errors.Join(err, snap.Close())
		}
	}()

	err = snap.acquireSource(ctx, opts)
	if err != nil {
		return nil, err
	}

	err = snap.readSource(ctx, opts.SourceAccount)
	if err != nil {
		return nil, err
	}

	err = snap.prepareCopies(ctx)
	if err != nil {
		return nil, err
	}

	config.notify("copied")

	err = snap.Revalidate(ctx)
	if err != nil {
		return nil, err
	}

	return snap, nil
}

// Revalidate rechecks rooted names and pinned descriptors as well as bytes.
// File checks detect changes; they cannot prove that the Java writer is stopped.
func (snap *snapshot) Revalidate(ctx context.Context) error {
	if snap.closed {
		return importError("closed snapshot", signal.ErrInvalidAccountImport)
	}

	pathErr := snap.revalidatePaths(ctx)
	if pathErr != nil {
		return pathErr
	}

	for _, source := range snap.source.files {
		digest, err := source.stream(ctx, io.Discard, "hash:", snap.options)
		if err != nil {
			return err
		}

		if digest != source.digest {
			return importError(source.category+" hash", signal.ErrAccountImportSourceChanged)
		}
	}

	cacheErr := snap.revalidateCache(ctx)
	if cacheErr != nil {
		return cacheErr
	}

	return snap.revalidatePaths(ctx)
}

// Close is idempotent and releases partial acquisitions on every failure path.
// Rooted cleanup still addresses our workspace if its ancestor has been renamed.
func (snap *snapshot) Close() error {
	if snap.closed {
		return nil
	}

	snap.closed = true
	result := snap.source.close()
	result = errors.Join(result, snap.tempPins.close())

	if snap.tempRoot != nil {
		result = errors.Join(result, snap.tempRoot.Close())
	}

	if snap.tempParent != nil {
		if snap.tempName != "" {
			result = errors.Join(result, snap.tempParent.RemoveAll(snap.tempName))
		}

		result = errors.Join(result, snap.tempParent.Close())
	}

	if result != nil {
		return importError("snapshot cleanup", signal.ErrInvalidAccountImport)
	}

	return nil
}

func (snap *snapshot) acquireSource(ctx context.Context, opts signal.AccountImportOptions) error {
	var err error

	snap.sourcePath, err = filepath.Abs(opts.SourceDir)
	if err != nil {
		return importError("source path", signal.ErrInvalidAccountImport)
	}

	snap.root, err = snap.source.absoluteDirectory(ctx, snap.sourcePath)
	if err != nil {
		return err
	}

	snap.destination, err = inspectDestination(opts.DataDir)
	if err != nil {
		return err
	}

	return snap.checkDestination(ctx)
}

func (snap *snapshot) readSource(ctx context.Context, selector string) error {
	data, err := snap.source.directory(snap.root, "data")
	if err != nil {
		return err
	}

	registryJSON, err := snap.readJSON(ctx, data, "accounts.json", "registry")
	if err != nil {
		return err
	}

	err = strictJSON(registryJSON, &snap.registry)
	if err != nil {
		return err
	}

	snap.selected, err = selectSource(snap.registry, selector)
	if err != nil {
		return err
	}

	accountJSON, err := snap.readJSON(ctx, data, *snap.selected.Path, "account")
	if err != nil {
		return err
	}

	err = strictJSON(accountJSON, &snap.account)
	if err != nil {
		return err
	}

	err = validateSourceAccount(&snap.account, snap.selected)
	if err != nil {
		return err
	}

	snap.databaseDir, err = snap.source.directory(data, *snap.selected.Path+".d")

	return err
}

func (snap *snapshot) prepareCopies(ctx context.Context) error {
	sqlFiles, err := snap.acquireSQLFiles()
	if err != nil {
		return err
	}

	snap.cache, err = snap.source.captureCache(ctx, snap.databaseDir, snap.options.limits.pathEntries)
	if err != nil {
		return err
	}

	err = snap.checkDestination(ctx)
	if err != nil {
		return err
	}

	err = snap.createWorkspace(ctx)
	if err != nil {
		return err
	}

	for _, source := range sqlFiles {
		err = snap.copySQLFile(ctx, source)
		if err != nil {
			return err
		}
	}

	return nil
}

func (snap *snapshot) readJSON(ctx context.Context, directory *os.File, name, category string) ([]byte, error) {
	source, err := snap.source.regularFile(directory, name, category, false)
	if err != nil {
		return nil, err
	}

	if source.stat.Size > snap.options.limits.jsonBytes {
		return nil, importError(category+" JSON byte limit", signal.ErrInvalidAccountImport)
	}

	var content bytes.Buffer

	source.digest, err = source.stream(ctx, &content, "read:", snap.options)
	if err != nil {
		return nil, err
	}

	return content.Bytes(), nil
}

func (snap *snapshot) acquireSQLFiles() ([]*sourceFile, error) {
	var files []*sourceFile

	remaining := snap.options.limits.sqlBytes
	for _, entry := range []struct {
		name, category string
		optional       bool
	}{
		{"account.db", "database", false},
		{"account.db-wal", "WAL", true},
		{"account.db-shm", "SHM", true},
	} {
		source, err := snap.source.regularFile(snap.databaseDir, entry.name, entry.category, entry.optional)
		if err != nil {
			return nil, err
		}

		if source == nil {
			continue
		}

		if source.stat.Size < 0 || source.stat.Size > remaining {
			return nil, importError(entry.category+" SQL byte limit", signal.ErrInvalidAccountImport)
		}

		remaining -= source.stat.Size
		files = append(files, source)
	}

	return files, nil
}

func (snap *snapshot) workspaceParent(ctx context.Context) (string, *os.File, error) {
	parentPath := snap.options.tempDir
	if parentPath == "" {
		parentPath = os.TempDir()
	}

	parentPath, err := filepath.Abs(parentPath)
	if err != nil {
		return "", nil, importError("temporary directory", signal.ErrInvalidAccountImport)
	}

	// A temp directory may be an ancestor (normally /tmp), but may never be
	// inside the source or destination. Check before creating even one entry.
	if parentPath == snap.sourcePath || strings.HasPrefix(parentPath, snap.sourcePath+"/") ||
		parentPath == snap.destination.resolved || strings.HasPrefix(parentPath, snap.destination.resolved+"/") {
		return "", nil, importError("temporary directory overlap", signal.ErrInvalidAccountImport)
	}

	parent, err := snap.tempPins.absoluteDirectory(ctx, parentPath)
	if err != nil {
		return "", nil, err
	}

	err = snap.checkTemporaryAliases()
	if err != nil {
		return "", nil, err
	}

	return parentPath, parent, nil
}

func (snap *snapshot) createWorkspace(ctx context.Context) error {
	parentPath, parent, err := snap.workspaceParent(ctx)
	if err != nil {
		return err
	}

	snap.tempParent, err = os.OpenRoot(parentPath)
	if err != nil {
		return importError("temporary root", signal.ErrInvalidAccountImport)
	}

	pinned, err := parent.Stat()
	if err != nil {
		return importError("temporary parent metadata", signal.ErrInvalidAccountImport)
	}

	opened, err := snap.tempParent.Stat(".")
	if err != nil || !os.SameFile(pinned, opened) {
		return importError("temporary parent binding", signal.ErrAccountImportSourceChanged)
	}

	name := "go-signal-import-" + rand.Text()
	workspace := filepath.Join(parentPath, name)

	err = snap.checkPhysicalContainment(ctx, workspace)
	if err != nil {
		return err
	}

	err = snap.tempParent.Mkdir(name, snapshotDirectoryMode)
	if err != nil {
		return importError("private workspace creation", signal.ErrInvalidAccountImport)
	}

	snap.tempName = name
	snap.workspace = workspace
	snap.sqlPath = filepath.Join(snap.workspace, "account.db")

	snap.tempRoot, err = snap.tempParent.OpenRoot(snap.tempName)
	if err != nil {
		return importError("private workspace open", signal.ErrInvalidAccountImport)
	}

	return nil
}

func (snap *snapshot) copySQLFile(ctx context.Context, source *sourceFile) error {
	target, err := snap.tempRoot.OpenFile(source.name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, snapshotFileMode)
	if err != nil {
		return importError("private SQL file", signal.ErrInvalidAccountImport)
	}

	source.digest, err = source.stream(ctx, target, "copy:", snap.options)
	closeErr := target.Close()

	if err != nil {
		return err
	}

	if closeErr != nil {
		return importError("private SQL close", signal.ErrInvalidAccountImport)
	}

	return nil
}

func (source *sourceFile) stream(ctx context.Context, target io.Writer, operation string,
	opts snapshotOptions,
) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte

	bindingErr := source.revalidate()
	if bindingErr != nil {
		return digest, importError(source.category+" binding", bindingErr)
	}

	before, err := statFile(source.file)
	if err != nil || !sameFileStamp(source.stat, before) {
		return digest, importError(source.category+" metadata", signal.ErrAccountImportSourceChanged)
	}

	hash := sha256.New()

	total, err := source.streamBytes(ctx, io.MultiWriter(target, hash), operation, opts)
	if err != nil {
		return digest, err
	}

	after, err := statFile(source.file)
	if err != nil || total != source.stat.Size || !sameFileStamp(source.stat, after) {
		return digest, importError(source.category+" changed", signal.ErrAccountImportSourceChanged)
	}

	copy(digest[:], hash.Sum(nil))

	return digest, nil
}

func (snap *snapshot) revalidatePaths(ctx context.Context) error {
	contextErr := ctx.Err()
	if contextErr != nil {
		return importError("snapshot canceled", contextErr)
	}

	sourceErr := snap.source.revalidateBindings(ctx)
	if sourceErr != nil {
		return sourceErr
	}

	destinationErr := snap.destination.revalidate()
	if destinationErr != nil {
		return importError("destination binding", destinationErr)
	}

	tempErr := snap.tempPins.revalidateBindings(ctx)
	if tempErr != nil {
		return tempErr
	}

	overlapErr := snap.checkDestination(ctx)
	if overlapErr != nil {
		if ctx.Err() != nil {
			return importError("snapshot canceled", ctx.Err())
		}

		return importError("destination changed", signal.ErrAccountImportSourceChanged)
	}

	return nil
}

func (snap *snapshot) revalidateCache(ctx context.Context) error {
	var current sourceTree
	defer func() { _ = current.close() }()

	entries, err := current.captureCache(ctx, snap.databaseDir, snap.options.limits.pathEntries)
	if err != nil {
		if ctx.Err() != nil {
			return importError("snapshot canceled", ctx.Err())
		}

		return importError("message cache changed", signal.ErrAccountImportSourceChanged)
	}

	less := func(left, right cacheEntry) int { return strings.Compare(left.path, right.path) }
	slices.SortFunc(entries, less)
	slices.SortFunc(snap.cache, less)

	if !slices.EqualFunc(entries, snap.cache, func(left, right cacheEntry) bool {
		return left.path == right.path && sameFileStamp(left.stat, right.stat)
	}) {
		return importError("message cache changed", signal.ErrAccountImportSourceChanged)
	}

	return nil
}

func (source *sourceFile) streamBytes(ctx context.Context, writer io.Writer, operation string,
	opts snapshotOptions,
) (int64, error) {
	reader := io.NewSectionReader(source.file, 0, source.stat.Size+1)
	buffer := make([]byte, snapshotCopyBuffer)

	var total int64

	for {
		err := ctx.Err()
		if err != nil {
			return total, importError("snapshot canceled", err)
		}

		count, readErr := reader.Read(buffer)

		total += int64(count)
		if total > source.stat.Size {
			return total, importError(source.category+" grew", signal.ErrAccountImportSourceChanged)
		}

		if count > 0 {
			_, writeErr := writer.Write(buffer[:count])
			if writeErr != nil {
				return total, importError(source.category+" copy", signal.ErrInvalidAccountImport)
			}

			opts.notify(operation + source.category)
		}

		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return total, importError(source.category+" read", signal.ErrInvalidAccountImport)
			}

			break
		}
	}

	err := ctx.Err()
	if err != nil {
		return total, importError("snapshot canceled", err)
	}

	return total, nil
}

func (snap *snapshot) checkTemporaryAliases() error {
	rootStat, err := statFile(snap.root)
	if err != nil {
		return err
	}

	for _, pin := range snap.tempPins.directories {
		if sameNode(rootStat, pin.stat) {
			return importError("temporary directory alias", signal.ErrInvalidAccountImport)
		}
	}

	return nil
}
