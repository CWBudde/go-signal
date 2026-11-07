//go:build linux && (cgo || libsignal_go)

package accountimport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/store"
	"golang.org/x/sys/unix"
)

type pathPin struct {
	parent *os.File
	name   string
	file   *os.File
	stat   unix.Stat_t
}

type sourceTree struct {
	directories []*pathPin
	files       []*sourceFile
	absent      []pathPin
}

func sameNode(left, right unix.Stat_t) bool {
	return left.Dev == right.Dev && left.Ino == right.Ino && left.Mode == right.Mode
}

func sameFileStamp(left, right unix.Stat_t) bool {
	return sameNode(left, right) && left.Size == right.Size && left.Mtim == right.Mtim && left.Ctim == right.Ctim
}

func statFile(file *os.File) (unix.Stat_t, error) {
	var stat unix.Stat_t

	err := unix.Fstat(int(file.Fd()), &stat)
	if err != nil {
		return stat, importError("file metadata", signal.ErrInvalidAccountImport)
	}

	return stat, nil
}

func statAt(parent *os.File, name string) (unix.Stat_t, error) {
	var stat unix.Stat_t

	err := unix.Fstatat(int(parent.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if err != nil {
		return stat, fmt.Errorf("source metadata: %w", err)
	}

	return stat, nil
}

func (tree *sourceTree) directory(parent *os.File, name string) (*os.File, error) {
	parentFD := unix.AT_FDCWD
	if parent != nil {
		parentFD = int(parent.Fd())
	}

	flags := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK

	descriptor, err := unix.Openat(parentFD, name, flags, 0)
	if err != nil {
		return nil, importError("source directory", signal.ErrInvalidAccountImport)
	}

	file := os.NewFile(uintptr(descriptor), "source directory")

	stat, err := statFile(file)
	if err != nil {
		_ = file.Close()
		return nil, err
	}

	tree.directories = append(tree.directories, &pathPin{parent: parent, name: name, file: file, stat: stat})

	return file, nil
}

func (tree *sourceTree) absoluteDirectory(ctx context.Context, path string) (*os.File, error) {
	directory, err := tree.directory(nil, "/")
	if err != nil {
		return nil, err
	}

	for component := range strings.SplitSeq(strings.TrimPrefix(path, "/"), "/") {
		err = ctx.Err()
		if err != nil {
			return nil, importError("source traversal canceled", err)
		}

		if component == "" {
			continue
		}

		directory, err = tree.directory(directory, component)
		if err != nil {
			return nil, err
		}
	}

	return directory, nil
}

func (tree *sourceTree) regularFile(parent *os.File, name, category string, optional bool) (*sourceFile, error) {
	stat, err := statAt(parent, name)
	if optional && errors.Is(err, unix.ENOENT) {
		tree.absent = append(tree.absent, pathPin{parent: parent, name: name})
		return nil, nil //nolint:nilnil // A pinned optional absence is a successful acquisition.
	}

	// Inspect before open too: O_NONBLOCK prevents a swapped FIFO from blocking,
	// and O_NOFOLLOW prevents a swapped link from being followed.
	if err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return nil, importError(category+" file type", signal.ErrInvalidAccountImport)
	}

	descriptor, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, importError(category+" open", signal.ErrInvalidAccountImport)
	}

	file := os.NewFile(uintptr(descriptor), category)

	opened, err := statFile(file)
	if err != nil || !sameFileStamp(stat, opened) {
		_ = file.Close()
		return nil, importError(category+" binding", signal.ErrAccountImportSourceChanged)
	}

	source := &sourceFile{pathPin: pathPin{parent: parent, name: name, file: file, stat: stat}, category: category}
	tree.files = append(tree.files, source)

	return source, nil
}

func (pin *pathPin) revalidate() error {
	if pin.parent != nil {
		current, err := statAt(pin.parent, pin.name)
		if err != nil || !sameNode(pin.stat, current) {
			return signal.ErrAccountImportSourceChanged
		}
	}

	held, err := statFile(pin.file)
	if err != nil || !sameNode(pin.stat, held) {
		return signal.ErrAccountImportSourceChanged
	}

	return nil
}

func (tree *sourceTree) revalidateBindings(ctx context.Context) error {
	for _, directory := range tree.directories {
		err := ctx.Err()
		if err != nil {
			return importError("source traversal canceled", err)
		}

		err = directory.revalidate()
		if err != nil {
			return importError("directory binding", err)
		}
	}

	for _, absent := range tree.absent {
		_, statErr := statAt(absent.parent, absent.name)
		if !errors.Is(statErr, unix.ENOENT) {
			return importError("optional file appeared", signal.ErrAccountImportSourceChanged)
		}
	}

	for _, source := range tree.files {
		err := source.revalidate()
		if err != nil {
			return importError("source file binding", err)
		}
	}

	return nil
}

func (tree *sourceTree) close() error {
	var result error
	for _, source := range tree.files {
		result = errors.Join(result, source.file.Close())
	}

	for _, directory := range slices.Backward(tree.directories) {
		result = errors.Join(result, directory.file.Close())
	}

	tree.files, tree.directories = nil, nil

	if result != nil {
		return importError("close source", signal.ErrInvalidAccountImport)
	}

	return nil
}

type cacheEntry struct {
	path string
	stat unix.Stat_t
}

func (tree *sourceTree) captureCache(ctx context.Context, directory *os.File, limit int) ([]cacheEntry, error) {
	stat, err := statAt(directory, "msg-cache")
	if errors.Is(err, unix.ENOENT) {
		tree.absent = append(tree.absent, pathPin{parent: directory, name: "msg-cache"})
		return nil, nil
	}

	if err != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return nil, importError("message cache directory", signal.ErrInvalidAccountImport)
	}

	cache, err := tree.directory(directory, "msg-cache")
	if err != nil {
		return nil, err
	}

	var entries []cacheEntry

	err = tree.cacheEntries(ctx, cache, "", &entries, limit, 0)
	if err != nil {
		return nil, err
	}

	return entries, nil
}

func eachDirectoryEntry(ctx context.Context, directory *os.File, visit func(os.DirEntry) error) error {
	for {
		contextErr := ctx.Err()
		if contextErr != nil {
			return importError("directory scan canceled", contextErr)
		}

		batch, err := directory.ReadDir(snapshotDirectoryBatch)
		if err != nil && !errors.Is(err, io.EOF) {
			return importError("directory listing", signal.ErrInvalidAccountImport)
		}

		for _, entry := range batch {
			visitErr := visit(entry)
			if visitErr != nil {
				return visitErr
			}
		}

		if errors.Is(err, io.EOF) {
			return nil
		}
	}
}

func (tree *sourceTree) cacheEntries(ctx context.Context, directory *os.File, prefix string,
	entries *[]cacheEntry, limit, depth int,
) error {
	if depth > snapshotDirectoryDepth {
		return importError("message cache depth", signal.ErrInvalidAccountImport)
	}

	return eachDirectoryEntry(ctx, directory, func(entry os.DirEntry) error {
		if len(*entries) >= limit {
			return importError("message cache entry limit", signal.ErrInvalidAccountImport)
		}

		stat, err := statAt(directory, entry.Name())
		if err != nil || (stat.Mode&unix.S_IFMT != unix.S_IFDIR && stat.Mode&unix.S_IFMT != unix.S_IFREG) {
			return importError("message cache file type", signal.ErrInvalidAccountImport)
		}

		path := filepath.Join(prefix, entry.Name())

		*entries = append(*entries, cacheEntry{path: path, stat: stat})
		if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
			return nil
		}

		child, err := tree.directory(directory, entry.Name())
		if err != nil {
			return err
		}

		return tree.cacheEntries(ctx, child, path, entries, limit, depth+1)
	})
}

type destinationPin struct {
	path string
	info os.FileInfo
}

type destinationPath struct {
	absolute, resolved string
	pins               []destinationPin
}

func inspectDestination(path string) (destinationPath, error) {
	absolute, err := destinationAbsolute(path)
	if err != nil {
		return destinationPath{}, err
	}

	destination := destinationPath{absolute: absolute}

	destination.pins, err = destinationBindings(absolute)
	if err != nil {
		return destinationPath{}, err
	}

	ancestor, missing, err := destinationAncestor(absolute)
	if err != nil {
		return destinationPath{}, err
	}

	resolved, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return destinationPath{}, importError("destination alias", signal.ErrInvalidAccountImport)
	}

	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return destinationPath{}, importError("destination directory", signal.ErrInvalidAccountImport)
	}

	for _, component := range slices.Backward(missing) {
		resolved = filepath.Join(resolved, component)
	}

	destination.resolved = resolved

	return destination, nil
}

func destinationAbsolute(path string) (string, error) {
	if path == "" {
		path = store.DefaultDir()
	}

	if rest, expand := strings.CutPrefix(path, "~/"); expand {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", importError("destination home", signal.ErrInvalidAccountImport)
		}

		path = filepath.Join(home, rest)
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", importError("destination path", signal.ErrInvalidAccountImport)
	}

	return absolute, nil
}

func destinationBindings(absolute string) ([]destinationPin, error) {
	var pins []destinationPin

	ancestor := "/"

	for component := range strings.SplitSeq(strings.TrimPrefix(absolute, "/"), "/") {
		if component == "" {
			continue
		}

		ancestor = filepath.Join(ancestor, component)

		info, err := os.Lstat(ancestor)
		if errors.Is(err, os.ErrNotExist) {
			break
		}

		if err != nil {
			return nil, importError("destination metadata", signal.ErrInvalidAccountImport)
		}

		pins = append(pins, destinationPin{path: ancestor, info: info})
	}

	return pins, nil
}

func destinationAncestor(path string) (string, []string, error) {
	var missing []string

	for {
		_, err := os.Lstat(path)
		if err == nil {
			return path, missing, nil
		}

		if !errors.Is(err, os.ErrNotExist) {
			return "", nil, importError("destination ancestor", signal.ErrInvalidAccountImport)
		}

		missing = append(missing, filepath.Base(path))
		path = filepath.Dir(path)
	}
}

func pathsOverlap(left, right string) bool {
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/") ||
		left == "/" || right == "/"
}

func (destination destinationPath) revalidate() error {
	for _, pin := range destination.pins {
		info, err := os.Lstat(pin.path)
		if err != nil || !os.SameFile(pin.info, info) || pin.info.Mode() != info.Mode() {
			return signal.ErrAccountImportSourceChanged
		}
	}

	current, err := inspectDestination(destination.absolute)
	if err != nil || current.resolved != destination.resolved {
		return signal.ErrAccountImportSourceChanged
	}

	return nil
}

// Only identities are inspected in the destination: never open account databases
// or follow directory symlinks. This catches hard links to any required source file.
func (snap *snapshot) checkDestination(ctx context.Context) error {
	if pathsOverlap(snap.sourcePath, snap.destination.resolved) {
		return importError("source/destination overlap", signal.ErrInvalidAccountImport)
	}

	containmentErr := snap.checkPhysicalContainment(ctx, snap.workspace)
	if containmentErr != nil {
		return containmentErr
	}

	var tree sourceTree
	defer func() { _ = tree.close() }()

	ancestor, _, err := destinationAncestor(snap.destination.resolved)
	if err != nil {
		return err
	}

	parent, err := tree.absoluteDirectory(ctx, ancestor)
	if err != nil {
		return err
	}

	err = snap.checkAncestorAliases(&tree)
	if err != nil {
		return err
	}

	if ancestor == snap.destination.resolved {
		for _, directory := range snap.source.directories {
			if sameNode(tree.directories[len(tree.directories)-1].stat, directory.stat) {
				return importError("destination/source alias", signal.ErrInvalidAccountImport)
			}
		}

		remaining := snap.options.limits.pathEntries

		return snap.scanDestination(ctx, &tree, parent, &remaining, 0)
	}

	return nil
}

func (snap *snapshot) checkAncestorAliases(tree *sourceTree) error {
	rootStat, err := statFile(snap.root)
	if err != nil {
		return err
	}

	for _, directory := range tree.directories {
		if sameNode(rootStat, directory.stat) {
			return importError("source/destination alias", signal.ErrInvalidAccountImport)
		}
	}

	return nil
}

func (snap *snapshot) scanDestination(ctx context.Context, tree *sourceTree, directory *os.File,
	remaining *int, depth int,
) error {
	if depth > snapshotDirectoryDepth {
		return importError("destination depth", signal.ErrInvalidAccountImport)
	}

	return eachDirectoryEntry(ctx, directory, func(entry os.DirEntry) error {
		*remaining--
		if *remaining < 0 {
			return importError("destination entry limit", signal.ErrInvalidAccountImport)
		}

		stat, err := statAt(directory, entry.Name())
		if err != nil {
			return importError("destination entry", signal.ErrInvalidAccountImport)
		}

		err = snap.checkEntryAliases(stat)
		if err != nil {
			return err
		}

		if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
			return nil
		}

		child, err := tree.directory(directory, entry.Name())
		if err != nil {
			return err
		}

		return snap.scanDestination(ctx, tree, child, remaining, depth+1)
	})
}

func (snap *snapshot) checkEntryAliases(stat unix.Stat_t) error {
	for _, source := range snap.source.files {
		if sameNode(stat, source.stat) {
			return importError("hard-linked source file", signal.ErrInvalidAccountImport)
		}
	}

	for _, source := range snap.source.directories {
		if sameNode(stat, source.stat) {
			return importError("source directory alias", signal.ErrInvalidAccountImport)
		}
	}

	return nil
}
