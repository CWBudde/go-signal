//go:build cgo || libsignal_go

package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/store"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// errNoDevice means signalmeow looked a device up before storing one.
var errNoDevice = errors.New("link store: no device stored yet")

// LinkStore is the signalmeow DeviceStore used while linking. The account's ACI (and so its
// directory) is only known once the server confirms the new device, so PutDevice takes the
// account lock and opens <data-dir>/<aci>/account.db at that point.
type LinkStore struct {
	dir *Dir
	log zerolog.Logger

	lock  *Lock
	store *Store
}

var _ store.DeviceStore = (*LinkStore)(nil)

// NewLinkStore returns a LinkStore on d.
func (d *Dir) NewLinkStore(log zerolog.Logger) *LinkStore {
	return &LinkStore{dir: d, log: log}
}

// PutDevice locks and opens the account database for data.ACI, then stores data in it.
func (l *LinkStore) PutDevice(ctx context.Context, data *store.DeviceData) error {
	_, err := l.open(ctx, data.ACI)
	if err != nil {
		return err
	}

	err = l.store.Devices.PutDevice(ctx, data)
	if err != nil {
		return fmt.Errorf("store device: %w", err)
	}

	return nil
}

// StoreProvisionedDevice locks and opens the account database for data.ACI, then stores the
// linked device and replaces the account's previous protocol state in one transaction. If
// that fails, a database and lock opened by this call are released again.
func (l *LinkStore) StoreProvisionedDevice(
	ctx context.Context, data *store.DeviceData, keys store.ProvisioningPreKeys,
) (*store.Device, error) {
	opened, err := l.open(ctx, data.ACI)
	if err != nil {
		return nil, err
	}

	device, err := l.store.Devices.StoreProvisionedDevice(ctx, data, keys)
	if err != nil {
		if opened {
			err = errors.Join(err, l.Close())
		}

		return nil, fmt.Errorf("store linked device: %w", err)
	}

	return device, nil
}

// DeviceByACI looks the device up in the database opened by PutDevice.
func (l *LinkStore) DeviceByACI(ctx context.Context, aci uuid.UUID) (*store.Device, error) {
	if l.store == nil {
		return nil, errNoDevice
	}

	device, err := l.store.DeviceByACI(ctx, aci)
	if err != nil {
		return nil, fmt.Errorf("load device: %w", err)
	}

	return device, nil
}

// DeviceByPNI looks the device up in the database opened by PutDevice.
func (l *LinkStore) DeviceByPNI(ctx context.Context, pni uuid.UUID) (*store.Device, error) {
	if l.store == nil {
		return nil, errNoDevice
	}

	device, err := l.store.Devices.DeviceByPNI(ctx, pni)
	if err != nil {
		return nil, fmt.Errorf("load device: %w", err)
	}

	return device, nil
}

// Take hands the opened database and the held lock to the caller, who must close and release
// them. Both are nil if PutDevice never ran.
func (l *LinkStore) Take() (*Store, *Lock) {
	opened, lock := l.store, l.lock
	l.store, l.lock = nil, nil

	return opened, lock
}

// Close releases whatever Take didn't hand out.
func (l *LinkStore) Close() error {
	opened, lock := l.Take()

	var errs []error

	if opened != nil {
		errs = append(errs, opened.Close())
	}

	if lock != nil {
		errs = append(errs, lock.Unlock())
	}

	return errors.Join(errs...)
}

// open takes the account lock and opens the database for aci unless that already happened.
// It reports whether this call opened them.
func (l *LinkStore) open(ctx context.Context, aci uuid.UUID) (bool, error) {
	if l.store != nil {
		return false, nil
	}

	lock, err := l.dir.Lock(aci.String())
	if err != nil {
		return false, err
	}

	opened, err := l.dir.OpenAccount(ctx, aci.String(), l.log)
	if err != nil {
		_ = lock.Unlock()

		return false, err
	}

	l.lock, l.store = lock, opened

	return true, nil
}
