package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// accountsVersion is the schema version of accounts.json.
const accountsVersion = 1

// ErrAccountsVersion means accounts.json was written by a newer go-signal.
var ErrAccountsVersion = errors.New("unsupported accounts.json version")

// AccountEntry is one linked account in accounts.json.
type AccountEntry struct {
	Number   string `json:"number"`
	ACI      string `json:"aci"`
	PNI      string `json:"pni,omitempty"`
	DeviceID int    `json:"deviceId"`
	// DeviceName is the name the device was linked with.
	DeviceName string    `json:"deviceName,omitempty"`
	LinkedAt   time.Time `json:"linkedAt,omitzero"`
	// UnlinkedAt is when the server was found to no longer accept the device (unlinked on the
	// phone); zero while linked. Relinking (PutAccount) clears it.
	UnlinkedAt time.Time `json:"unlinkedAt,omitzero"`
}

type accountsDoc struct {
	Version  int            `json:"version"`
	Accounts []AccountEntry `json:"accounts"`
}

// Accounts returns the entries of accounts.json in link order; a missing file means none.
func (d *Dir) Accounts() ([]AccountEntry, error) {
	var accounts []AccountEntry

	err := d.withRegistry(func(reg *registry) error {
		var err error

		accounts, err = reg.accounts()

		return err
	})

	return accounts, err
}

// PutAccount adds entry to accounts.json, replacing an existing entry with the same ACI.
func (d *Dir) PutAccount(entry AccountEntry) error {
	return d.withRegistry(func(reg *registry) error {
		accounts, err := reg.accounts()
		if err != nil {
			return err
		}

		replaced := false

		for i := range accounts {
			if accounts[i].ACI == entry.ACI {
				accounts[i] = entry
				replaced = true
			}
		}

		if !replaced {
			accounts = append(accounts, entry)
		}

		return reg.write(accounts)
	})
}

// MarkUnlinked records that the account with the given ACI was found unlinked at unlinkedAt. An
// earlier mark is kept, and a missing entry is not an error (the account may have been removed
// meanwhile).
func (d *Dir) MarkUnlinked(aci string, unlinkedAt time.Time) error {
	return d.withRegistry(func(reg *registry) error {
		accounts, err := reg.accounts()
		if err != nil {
			return err
		}

		for i := range accounts {
			if accounts[i].ACI != aci || !accounts[i].UnlinkedAt.IsZero() {
				continue
			}

			accounts[i].UnlinkedAt = unlinkedAt

			return reg.write(accounts)
		}

		return nil
	})
}

// RemoveAccount deletes the account with the given ACI: under the registry lock its
// accounts.json entry, then it moves the directory aside, so that nothing selects or reopens
// the account from then on; then it deletes the directory including the database. The caller
// should hold the account's Lock, which stays valid until Unlock although its file is gone. A
// missing entry or directory is not an error.
func (d *Dir) RemoveAccount(aci string) error {
	var removed string

	err := d.withRegistry(func(reg *registry) error {
		accounts, err := reg.accounts()
		if err != nil {
			return err
		}

		kept := slices.DeleteFunc(accounts, func(entry AccountEntry) bool { return entry.ACI == aci })

		err = reg.write(kept)
		if err != nil {
			return err
		}

		removed, err = reg.moveAside(d.AccountDir(aci))

		return err
	})
	if err != nil {
		return err
	}

	if removed == "" {
		return nil
	}

	err = os.RemoveAll(removed)
	if err != nil {
		return fmt.Errorf("remove account data: %w", err)
	}

	return nil
}

// moveAside moves path into removedDir and returns its new path, or "" if there is nothing at
// path.
func (reg *registry) moveAside(path string) (string, error) {
	_, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("remove account data: %w", err)
	}

	token, err := newToken()
	if err != nil {
		return "", err
	}

	parent := filepath.Join(reg.d.path, removedDir)

	err = reg.d.mkdir(parent)
	if err != nil {
		return "", fmt.Errorf("remove account data: %w", err)
	}

	target := filepath.Join(parent, token)

	err = os.Rename(path, target)
	if err == nil {
		err = syncDir(reg.d.path)
	}

	if err != nil {
		return "", fmt.Errorf("remove account data: %w", err)
	}

	return target, nil
}

// accounts reads accounts.json.
func (reg *registry) accounts() ([]AccountEntry, error) {
	path := filepath.Join(reg.d.path, accountsFile)

	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("read %s: %w", accountsFile, err)
	}

	info, err := os.Stat(path)
	if err == nil {
		reg.d.checkPerm(path, info)
	}

	var doc accountsDoc

	err = json.Unmarshal(raw, &doc)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", accountsFile, err)
	}

	if doc.Version != accountsVersion {
		return nil, fmt.Errorf("%w: %d", ErrAccountsVersion, doc.Version)
	}

	return doc.Accounts, nil
}

// write replaces accounts.json atomically (temp file + rename) and durably.
func (reg *registry) write(accounts []AccountEntry) error {
	raw, err := json.MarshalIndent(accountsDoc{Version: accountsVersion, Accounts: accounts}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", accountsFile, err)
	}

	tmp, err := os.CreateTemp(reg.d.path, accountsFile+".*")
	if err != nil {
		return fmt.Errorf("write %s: %w", accountsFile, err)
	}

	// CreateTemp uses 0600 already; the rename keeps that mode.
	_, err = tmp.Write(append(raw, '\n'))
	if err == nil {
		err = tmp.Sync()
	}

	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}

	if err == nil {
		err = os.Rename(tmp.Name(), filepath.Join(reg.d.path, accountsFile))
	}

	if err != nil {
		_ = os.Remove(tmp.Name())

		return fmt.Errorf("write %s: %w", accountsFile, err)
	}

	err = syncDir(reg.d.path)
	if err != nil {
		return fmt.Errorf("write %s: %w", accountsFile, err)
	}

	return nil
}
