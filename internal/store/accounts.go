package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// accountsVersion is the schema version of accounts.json.
const accountsVersion = 1

// ErrAccountsVersion means accounts.json was written by a newer go-signal.
var ErrAccountsVersion = errors.New("unsupported accounts.json version")

// AccountEntry is one linked account in accounts.json.
type AccountEntry struct {
	Number   string    `json:"number"`
	ACI      string    `json:"aci"`
	PNI      string    `json:"pni,omitempty"`
	DeviceID int       `json:"deviceId"`
	LinkedAt time.Time `json:"linkedAt,omitzero"`
}

type accountsDoc struct {
	Version  int            `json:"version"`
	Accounts []AccountEntry `json:"accounts"`
}

// Accounts returns the entries of accounts.json in link order; a missing file means none.
func (d *Dir) Accounts() ([]AccountEntry, error) {
	path := filepath.Join(d.path, accountsFile)

	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("read %s: %w", accountsFile, err)
	}

	info, err := os.Stat(path)
	if err == nil {
		d.checkPerm(path, info)
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

// PutAccount adds entry to accounts.json, replacing an existing entry with the same ACI.
func (d *Dir) PutAccount(entry AccountEntry) error {
	accounts, err := d.Accounts()
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

	return d.writeAccounts(accounts)
}

// writeAccounts replaces accounts.json atomically (temp file + rename).
func (d *Dir) writeAccounts(accounts []AccountEntry) error {
	raw, err := json.MarshalIndent(accountsDoc{Version: accountsVersion, Accounts: accounts}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", accountsFile, err)
	}

	tmp, err := os.CreateTemp(d.path, accountsFile+".*")
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
		err = os.Rename(tmp.Name(), filepath.Join(d.path, accountsFile))
	}

	if err != nil {
		_ = os.Remove(tmp.Name())

		return fmt.Errorf("write %s: %w", accountsFile, err)
	}

	return nil
}
