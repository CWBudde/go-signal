//go:build cgo || libsignal_go

package accountimport

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/protobuf/signalpb"
	mstore "github.com/cwbudde/mautrix-signal/pkg/signalmeow/store"
	"google.golang.org/protobuf/proto"
)

var (
	// ErrUnsupportedSharingMode is a phone number sharing mode without a faithful AccountRecord
	// value: signal-cli turns CONTACTS into NOBODY.
	ErrUnsupportedSharingMode = errors.New("unsupported phone number sharing mode")
	// ErrInvalidAccountRecord is a source account record or profile key that can't be used.
	ErrInvalidAccountRecord = errors.New("invalid source account record")
)

// AccountConfig is signal-cli's privacy configuration (ConfigurationStore). A nil value is
// a missing SQL row; PhoneNumberSharingMode is "" when missing.
type AccountConfig struct {
	ReadReceipts                   *bool
	TypingIndicators               *bool
	UnidentifiedDeliveryIndicators *bool
	LinkPreviews                   *bool
	PhoneNumberUnlisted            *bool
	PhoneNumberSharingMode         string
}

// AccountOverlay builds the account record signal-cli would upload from its cached self
// storage record, its configuration and the self profile key (nil: none), following
// StorageSyncModels.localToRemoteRecord: missing receipt, typing, sealed-sender indicator and
// link preview settings are true, a missing unlisted setting is false and missing sharing is
// UNKNOWN. Sharing CONTACTS is refused (ErrUnsupportedSharingMode). The self recipient's
// profile name and username are not applied.
func AccountOverlay(cached []byte, cfg AccountConfig, selfProfileKey []byte) (*signalpb.AccountRecord, error) {
	record := &signalpb.AccountRecord{}

	err := proto.Unmarshal(cached, record)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidAccountRecord, err)
	}

	if selfProfileKey != nil {
		if len(selfProfileKey) != libsignalgo.ProfileKeyLength {
			return nil, fmt.Errorf("%w: profile key of %d bytes", ErrInvalidAccountRecord, len(selfProfileKey))
		}

		record.ProfileKey = bytes.Clone(selfProfileKey)
	}

	orDefault := func(value *bool, fallback bool) bool {
		if value == nil {
			return fallback
		}

		return *value
	}

	record.TypingIndicators = orDefault(cfg.TypingIndicators, true)
	record.ReadReceipts = orDefault(cfg.ReadReceipts, true)
	record.SealedSenderIndicators = orDefault(cfg.UnidentifiedDeliveryIndicators, true)
	record.LinkPreviews = orDefault(cfg.LinkPreviews, true)
	record.UnlistedPhoneNumber = orDefault(cfg.PhoneNumberUnlisted, false)

	switch cfg.PhoneNumberSharingMode {
	case "":
		record.PhoneNumberSharingMode = signalpb.AccountRecord_UNKNOWN
	case "EVERYBODY":
		record.PhoneNumberSharingMode = signalpb.AccountRecord_EVERYBODY
	case "NOBODY":
		record.PhoneNumberSharingMode = signalpb.AccountRecord_NOBODY
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedSharingMode, cfg.PhoneNumberSharingMode)
	}

	record.Username = ""

	return record, nil
}

// AccountGuards returns the privacy guards that protect effective, the record from
// AccountOverlay, against the storage service, armed at the source's manifest version. A
// field is protected when it differs from the cached record, or always unless fresh says
// that the cached record is known to be what the storage service has.
func AccountGuards(cached, effective *signalpb.AccountRecord, cfg AccountConfig, version uint64, fresh bool,
) []mstore.PrivacyGuard {
	local, remote := mstore.AccountPrivacyValues(effective), mstore.AccountPrivacyValues(cached)
	present := map[mstore.PrivacyField]bool{
		mstore.PrivacyFieldReadReceipts:           cfg.ReadReceipts != nil,
		mstore.PrivacyFieldTypingIndicators:       cfg.TypingIndicators != nil,
		mstore.PrivacyFieldSealedSenderIndicators: cfg.UnidentifiedDeliveryIndicators != nil,
		mstore.PrivacyFieldLinkPreviews:           cfg.LinkPreviews != nil,
		mstore.PrivacyFieldUnlistedPhoneNumber:    cfg.PhoneNumberUnlisted != nil,
		mstore.PrivacyFieldPhoneNumberSharingMode: cfg.PhoneNumberSharingMode != "",
		mstore.PrivacyFieldOwnProfileKey:          true,
	}

	var guards []mstore.PrivacyGuard

	for _, field := range mstore.AccountPrivacyFields {
		value, ok := local[field]
		if !ok || (fresh && bytes.Equal(value, remote[field])) {
			continue
		}

		guards = append(guards, mstore.PrivacyGuard{
			Kind: mstore.PrivacyEntityAccount, Field: field, LocalPresent: present[field], Value: value,
			Protected: true, ArmedVersion: version,
		})
	}

	return guards
}
