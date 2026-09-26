//go:build cgo || purego

package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/store"
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
	if l.store == nil {
		aci := data.ACI.String()

		lock, err := l.dir.Lock(aci)
		if err != nil {
			return err
		}

		opened, err := l.dir.OpenAccount(ctx, aci, l.log)
		if err != nil {
			_ = lock.Unlock()

			return err
		}

		l.lock, l.store = lock, opened
	}

	err := l.store.Devices.PutDevice(ctx, data)
	if err != nil {
		return fmt.Errorf("store device: %w", err)
	}

	return nil
}

// DeviceByACI looks the device up in the database opened by PutDevice.
func (l *LinkStore) DeviceByACI(ctx context.Context, aci uuid.UUID) (*store.Device, error) {
	if l.store == nil {
		return nil, errNoDevice
	}

	device, err := l.store.Devices.DeviceByACI(ctx, aci)
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
