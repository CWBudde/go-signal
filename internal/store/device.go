//go:build cgo || libsignal_go

package store

import (
	"context"
	"fmt"

	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	mstore "github.com/cwbudde/mautrix-signal/pkg/signalmeow/store"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

// DeviceByACI loads device data while preserving a saved, empty account record. The pinned
// signalmeow loader treats a zero-length protobuf as absent, even though an empty AccountRecord
// has meaningful defaults (including read receipts disabled). SQL NULL means not yet learned.
func (s *Store) DeviceByACI(ctx context.Context, aci uuid.UUID) (*mstore.Device, error) {
	device, err := s.Devices.DeviceByACI(ctx, aci)
	if err != nil {
		return nil, fmt.Errorf("load device: %w", err)
	}

	if device == nil || device.AccountRecord != nil {
		return device, nil
	}

	var (
		present bool
		raw     []byte
	)

	err = s.db.QueryRow(ctx,
		"SELECT account_record, account_record IS NOT NULL FROM signalmeow_device WHERE aci_uuid=$1",
		aci).Scan(&raw, &present)
	if err != nil {
		return nil, fmt.Errorf("load account record presence: %w", err)
	}

	if present {
		device.AccountRecord = &signalpb.AccountRecord{}
		// Read the blob too: storage sync may have written a populated record since the load.
		err = proto.Unmarshal(raw, device.AccountRecord)
		if err != nil {
			return nil, fmt.Errorf("decode account record: %w", err)
		}
	}

	return device, nil
}
