package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

const (
	intentFile    = "intent.json"
	ownerLockFile = "owner.lock"
	// ownerMarker in a staged account directory names the attempt that owns it.
	ownerMarker   = ".import-owner"
	intentVersion = 1
)

// ErrImportCollision means an account to publish clashes with an existing entry or directory.
var ErrImportCollision = errors.New("account already exists")

// importIntent commits an import attempt to publishing exactly this registry entry. Once it is
// durable, the attempt is finished, by the importer or by recovery, or reported as needing
// recovery; it is never silently dropped.
type importIntent struct {
	Version int          `json:"version"`
	Token   string       `json:"token"`
	Entry   AccountEntry `json:"entry"`
	// Digest is the SHA-256 of the staged account.db.
	Digest string `json:"digest"`
}

// Publication steps after which a test hook (Dir.hook) may stop the process.
const (
	stepAllocated  = "allocated"
	stepBuilt      = "built"
	stepMarked     = "marked"
	stepIntent     = "intent"
	stepRenamed    = "renamed"
	stepSynced     = "synced"
	stepRegistered = "registered"
	stepRetiring   = "retiring"
)

// step runs the test hook for the publication step name; production has none.
func (d *Dir) step(name string) error {
	if d.hook == nil {
		return nil
	}

	return d.hook(name)
}

// recover deletes the directories of removed accounts and finishes or cleans up every import
// attempt whose owner is gone. Attempts whose owner lock is held are still running and are left
// alone. It runs before every registry operation.
func (reg *registry) recover() error {
	reg.cleanRemoved()

	path := filepath.Join(reg.d.path, importsDir)

	entries, err := os.ReadDir(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("%w: read %s: %w", ErrRecoveryRequired, path, err)
	}

	for _, entry := range entries {
		err = reg.recoverAttempt(entry.Name())
		if err != nil {
			return err
		}
	}

	return nil
}

// cleanRemoved deletes what RemoveAccount moved aside but didn't get to delete.
func (reg *registry) cleanRemoved() {
	path := filepath.Join(reg.d.path, removedDir)

	entries, err := os.ReadDir(path)
	if err != nil {
		return
	}

	for _, entry := range entries {
		err = os.RemoveAll(filepath.Join(path, entry.Name()))
		if err != nil {
			reg.d.log.Warn("delete removed account data", "path", filepath.Join(path, entry.Name()), "error", err)
		}
	}
}

// recoverAttempt finishes or removes the abandoned import attempt name.
func (reg *registry) recoverAttempt(name string) error {
	stage := filepath.Join(reg.d.path, importsDir, name)

	if !isToken(name) {
		reg.d.log.Warn("unexpected entry in the imports directory; leaving it alone", "path", stage)

		return nil
	}

	owner, err := reg.lockOwner(stage)
	if errors.Is(err, errWouldBlock) || errors.Is(err, errNothingToRecover) {
		return nil // still running, or nothing left
	}

	if err != nil {
		return err
	}

	defer func() { _ = owner.Close() }()

	intent, err := readIntent(stage)
	if errors.Is(err, fs.ErrNotExist) {
		// Never committed to publishing: drop it.
		return reg.removeStage(stage)
	}

	if err != nil {
		return recoveryError(stage, err)
	}

	if intent.Version != intentVersion || intent.Token != name {
		return recoveryError(stage, fmt.Errorf("%w: intent for %q, version %d", errEvidence, intent.Token, intent.Version))
	}

	err = reg.rollForward(stage, intent)
	if err != nil {
		return recoveryError(stage, err)
	}

	reg.d.log.Info("finished an interrupted account import", "account", intent.Entry.Number)

	return nil
}

// errNothingToRecover means an attempt was only an empty directory, now removed.
var errNothingToRecover = errors.New("nothing to recover")

// lockOwner takes the owner lock of the attempt stage. An attempt that is still running is
// errWouldBlock, one that was only an empty directory errNothingToRecover.
func (reg *registry) lockOwner(stage string) (*os.File, error) {
	owner, err := os.OpenFile(filepath.Join(stage, ownerLockFile), os.O_RDWR, 0)
	if errors.Is(err, fs.ErrNotExist) {
		// Allocation stopped before the owner lock existed; only an empty directory is ours.
		if os.Remove(stage) != nil {
			reg.d.log.Warn("import attempt without an owner lock; leaving it alone", "path", stage)
		}

		return nil, errNothingToRecover
	}

	if err == nil {
		err = tryLock(owner)
		if err != nil {
			_ = owner.Close()
		}
	}

	if err != nil && !errors.Is(err, errWouldBlock) {
		return nil, recoveryError(stage, err)
	}

	return owner, err
}

// errEvidence means the files of an import attempt disagree with its intent.
var errEvidence = errors.New("evidence disagrees with the import intent")

func recoveryError(stage string, err error) error {
	return fmt.Errorf("%w: %s: %w; inspect or move it away before using this data dir", ErrRecoveryRequired, stage, err)
}

// rollForward publishes the account of intent from wherever the attempt stopped: still staged,
// already renamed into place, or already registered. Before the registry entry exists, the
// account's ownership marker and database digest must match the intent.
func (reg *registry) rollForward(stage string, intent importIntent) error {
	aci := intent.Entry.ACI
	staged, final := filepath.Join(stage, aci), reg.d.AccountDir(aci)

	accounts, err := reg.accounts()
	if err != nil {
		return err
	}

	if !slices.Contains(accounts, intent.Entry) {
		err = reg.publishAccount(stage, staged, final, intent, accounts)
		if err != nil {
			return err
		}
	}

	return reg.retire(stage, final, intent.Token)
}

// publishAccount moves the staged account into place, if it isn't there yet, and registers it.
func (reg *registry) publishAccount(stage, staged, final string, intent importIntent, accounts []AccountEntry) error {
	err := collision(accounts, intent.Entry)
	if err != nil {
		return err
	}

	stagedDir, finalMissing, err := locate(staged, final)
	if err != nil {
		return err
	}

	switch {
	case stagedDir && finalMissing:
		err = reg.moveStaged(stage, staged, final, intent)
	case !stagedDir && !finalMissing:
		err = checkStaged(final, intent)
	default:
		err = fmt.Errorf("%w: the account is at neither or both of %s and %s", errEvidence, staged, final)
	}

	if err != nil {
		return err
	}

	err = reg.write(append(accounts, intent.Entry))
	if err != nil {
		return err
	}

	return reg.d.step(stepRegistered)
}

// moveStaged checks the staged account of intent and renames it into place, durably.
func (reg *registry) moveStaged(stage, staged, final string, intent importIntent) error {
	err := checkStaged(staged, intent)
	if err == nil {
		err = renameNoReplace(staged, final)
	}

	if err == nil {
		err = reg.d.step(stepRenamed)
	}

	if err == nil {
		err = errors.Join(syncDir(stage), syncDir(reg.d.path))
	}

	if err != nil {
		return err
	}

	return reg.d.step(stepSynced)
}

// retire removes what only the attempt needed once its account is registered: the ownership
// marker, the intent and the stage directory.
func (reg *registry) retire(stage, final, token string) error {
	marker := filepath.Join(final, ownerMarker)

	owner, err := os.ReadFile(marker)
	if err == nil && string(owner) == token {
		err = os.Remove(marker)
		if err == nil {
			err = syncDir(final)
		}

		if err != nil {
			return err
		}
	}

	err = reg.d.step(stepRetiring)
	if err != nil {
		return err
	}

	err = os.Remove(filepath.Join(stage, intentFile))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("retire import intent: %w", err)
	}

	err = syncDir(stage)
	if err != nil {
		return err
	}

	return reg.removeStage(stage)
}

// removeStage deletes an import attempt directory whose owner lock the caller holds.
func (reg *registry) removeStage(stage string) error {
	err := os.RemoveAll(stage)
	if err != nil {
		return fmt.Errorf("remove import attempt: %w", err)
	}

	return syncDir(filepath.Dir(stage))
}

// collision refuses entry if the registry has another entry with its ACI, number or PNI.
func collision(accounts []AccountEntry, entry AccountEntry) error {
	for _, existing := range accounts {
		switch {
		case existing.ACI == entry.ACI:
			return fmt.Errorf("%w: %s is registered", ErrImportCollision, entry.ACI)
		case entry.Number != "" && existing.Number == entry.Number:
			return fmt.Errorf("%w: %s is registered as %s", ErrImportCollision, entry.Number, existing.ACI)
		case entry.PNI != "" && existing.PNI == entry.PNI:
			return fmt.Errorf("%w: PNI %s is registered as %s", ErrImportCollision, entry.PNI, existing.ACI)
		}
	}

	return nil
}

// checkStaged checks that the account directory dir belongs to intent's attempt and holds the
// database it was committed with.
func checkStaged(dir string, intent importIntent) error {
	owner, err := os.ReadFile(filepath.Join(dir, ownerMarker))
	if err != nil {
		return fmt.Errorf("%w: ownership marker: %w", errEvidence, err)
	}

	if string(owner) != intent.Token {
		return fmt.Errorf("%w: %s is owned by %q", errEvidence, dir, owner)
	}

	digest, err := fileDigest(filepath.Join(dir, dbFile))
	if err != nil {
		return fmt.Errorf("%w: %w", errEvidence, err)
	}

	if digest != intent.Digest {
		return fmt.Errorf("%w: database digest %s, intent has %s", errEvidence, digest, intent.Digest)
	}

	return nil
}

// locate reports whether the account directory is still staged and whether its final path is
// free.
func locate(staged, final string) (bool, bool, error) {
	stagedDir, err := isDir(staged)
	if err != nil {
		return false, false, err
	}

	_, err = os.Lstat(final)
	if errors.Is(err, fs.ErrNotExist) {
		return stagedDir, true, nil
	}

	if err != nil {
		return false, false, fmt.Errorf("inspect %s: %w", final, err)
	}

	return stagedDir, false, nil
}

// isDir reports whether path is a directory (not a symlink to one); missing is false.
func isDir(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", path, err)
	}

	if !info.IsDir() {
		return false, fmt.Errorf("%w: %s is not a directory", errEvidence, path)
	}

	return true, nil
}

func readIntent(stage string) (importIntent, error) {
	var intent importIntent

	raw, err := os.ReadFile(filepath.Join(stage, intentFile))
	if err != nil {
		return intent, err //nolint:wrapcheck // callers check fs.ErrNotExist and wrap
	}

	err = json.Unmarshal(raw, &intent)
	if err != nil {
		return intent, fmt.Errorf("parse %s: %w", intentFile, err)
	}

	return intent, nil
}

// fileDigest returns the hex SHA-256 of the file path.
func fileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("digest: %w", err)
	}

	defer func() { _ = file.Close() }()

	sum := sha256.New()

	_, err = io.Copy(sum, file)
	if err != nil {
		return "", fmt.Errorf("digest %s: %w", path, err)
	}

	return hex.EncodeToString(sum.Sum(nil)), nil
}

// writeDurable writes data to path through a synced temporary file, then syncs the directory.
func writeDurable(path string, data []byte) error {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}

	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}

	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}

	if err != nil {
		_ = os.Remove(tmp.Name())

		return fmt.Errorf("write %s: %w", path, err)
	}

	return syncDir(dir)
}
