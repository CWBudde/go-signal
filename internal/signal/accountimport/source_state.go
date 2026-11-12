//go:build linux && (cgo || libsignal_go)

package accountimport

import (
	"context"
	"strings"

	"github.com/cwbudde/go-signal/internal/signal"
)

//nolint:nilnil // SQL NULL is absence; explicit false uses a nonnil pointer.
func sourceBoolean(cell sourceValue) (*bool, error) {
	if cell.kind == sourceSQLNull {
		return nil, nil
	}

	number, ok := cell.value.(int64)
	if cell.kind != sourceSQLInteger || !ok || (number != 0 && number != 1) {
		return nil, importError("SQL boolean", signal.ErrInvalidAccountImport)
	}

	value := number == 1

	return &value, nil
}

func sourceBooleans(row sourceRow, names ...string) error {
	for _, name := range names {
		_, err := sourceBoolean(row.values[name])
		if err != nil {
			return err
		}
	}

	return nil
}

func (source *sourceDB) readConfiguration(ctx context.Context) error {
	source.config = AccountConfig{}
	source.manifestVersion = 0
	retrySeen := false

	err := source.each(ctx, "key_value", func(row sourceRow) error {
		retry, rowErr := source.applyConfiguration(row)
		retrySeen = retrySeen || retry

		return rowErr
	})
	if err != nil {
		return err
	}

	if !retrySeen {
		return importError("pending retry default", signal.ErrUnsupportedAccountImport)
	}

	return nil
}

func (source *sourceDB) applyConfiguration(row sourceRow) (bool, error) {
	name := row.text("key")

	cell := row.values["value"]
	if name == "retry-failed-messages" {
		value, err := sourceBoolean(cell)
		if err != nil {
			return false, err
		}

		if value == nil || *value {
			return false, importError("pending retry", signal.ErrUnsupportedAccountImport)
		}

		return true, nil
	}

	targets := map[string]**bool{
		"config-read-receipts":                    &source.config.ReadReceipts,
		"config-typing-indicators":                &source.config.TypingIndicators,
		"config-unidentified-delivery-indicators": &source.config.UnidentifiedDeliveryIndicators,
		"config-link-previews":                    &source.config.LinkPreviews,
		"config-phone-number-unlisted":            &source.config.PhoneNumberUnlisted,
		"unrestricted-unidentified-access":        nil,
	}
	if target, exists := targets[name]; exists {
		value, err := sourceBoolean(cell)
		if err != nil {
			return false, err
		}

		if target != nil {
			*target = value
		}

		return false, nil
	}

	return false, source.applyConfigurationValue(row)
}

func (source *sourceDB) applyConfigurationValue(row sourceRow) error {
	name := row.text("key")

	cell := row.values["value"]
	if name == "config-phone-number-sharing-mode" {
		return source.applySharing(cell)
	}

	if name == "storage-manifest-version" {
		if cell.kind == sourceSQLNull {
			return nil
		}

		if cell.kind != sourceSQLInteger || row.integer("value") < -1 {
			return importError("SQL manifest provenance", signal.ErrInvalidAccountImport)
		}

		if row.integer("value") >= 0 {
			source.manifestVersion = uint64(row.integer("value"))
		}

		return nil
	}

	kinds := map[string]string{
		"last-receive-timestamp": sourceSQLInteger, "last-applied-pni-change-server-timestamp": sourceSQLInteger,
		"last-recipients-refresh": sourceSQLInteger, "verification-session-id": sourceSQLText,
		"verification-session-number": sourceSQLText, "username-link-color": sourceSQLText, "cdsi-token": sourceSQLBlob,
	}

	expected, known := kinds[name]
	if !known {
		return importError("unknown SQL configuration", signal.ErrUnsupportedAccountImport)
	}

	if cell.kind != sourceSQLNull && cell.kind != expected {
		return importError("SQL metadata type", signal.ErrInvalidAccountImport)
	}

	return nil
}

func (source *sourceDB) applySharing(cell sourceValue) error {
	if cell.kind == sourceSQLNull {
		return nil
	}

	if cell.kind != sourceSQLText {
		return importError("SQL sharing type", signal.ErrInvalidAccountImport)
	}

	value, _ := cell.value.(string)
	switch value {
	case sourceSharingEverybody, sourceSharingNobody:
		source.config.PhoneNumberSharingMode = value
	case "CONTACTS":
		return importError("phone sharing", signal.ErrUnsupportedAccountImport)
	default:
		return importError("SQL sharing enum", signal.ErrInvalidAccountImport)
	}

	return nil
}

func validateRecipientRow(row sourceRow, snap *snapshot) error {
	err := sourceBooleans(row, "discoverable", "needs_pni_signature", "pni_signature_verified",
		"blocked", "archived", "profile_sharing", "hide_story", "hidden")
	if err != nil {
		return err
	}

	err = validateRecipientFields(row)
	if err != nil {
		return err
	}

	err = validateRecipientState(row)
	if err != nil {
		return err
	}

	return validateSelfRecipient(row, snap)
}

func validateRecipientFields(row sourceRow) error {
	invalid := func() error { return importError("SQL recipient fields", signal.ErrInvalidAccountImport) }
	if !row.null("number") && !sourceE164.MatchString(row.text("number")) {
		return invalid()
	}

	for _, name := range []string{"expiration_time", "expiration_time_version"} {
		if value := row.integer(name); value < 0 || value > 0xffffffff {
			return invalid()
		}
	}

	for name, length := range map[string]int{"profile_key": sourceProfileKeyBytes, "storage_id": sourceUUIDBytes} {
		if !row.null(name) && len(row.blob(name)) != length {
			return invalid()
		}
	}

	return validateRecipientSharing(row)
}

func validateRecipientSharing(row sourceRow) error {
	if row.null("profile_phone_number_sharing") {
		return nil
	}

	switch row.text("profile_phone_number_sharing") {
	case sourceSharingEverybody, "CONTACTS", sourceSharingNobody:
		return nil
	default:
		return importError("SQL recipient sharing", signal.ErrInvalidAccountImport)
	}
}

func validateRecipientState(row sourceRow) error {
	unsupported := func() error { return importError("recipient state", signal.ErrUnsupportedAccountImport) }

	for _, name := range []string{"mute_until", "archived", "hidden", "hide_story"} {
		if row.integer(name) != 0 {
			return unsupported()
		}
	}

	if !row.null(sourceACIColumn) {
		return nil
	}

	if row.integer("blocked") != 0 || row.integer("expiration_time") != 0 || row.integer("expiration_time_version") != 1 {
		return unsupported()
	}

	return validateAddresslessRecipient(row)
}

func validateAddresslessRecipient(row sourceRow) error {
	if !row.null(sourcePNIColumn) || !row.null("number") {
		return nil
	}

	if row.integer("profile_sharing") != 0 || row.integer("needs_pni_signature") != 0 || !row.null("profile_key") {
		return importError("recipient state", signal.ErrUnsupportedAccountImport)
	}

	return nil
}

func validateSelfRecipient(row sourceRow, snap *snapshot) error {
	own := map[string]string{
		sourceACIColumn: strings.ToLower(*snap.account.ACI.ServiceID),
		sourcePNIColumn: strings.ToLower(*snap.account.PNI.ServiceID),
		"number":        *snap.account.Number,
	}

	self := false
	for name, value := range own {
		self = self || strings.EqualFold(row.text(name), value)
	}

	if !self {
		return nil
	}

	for name, value := range own {
		if !row.null(name) && !strings.EqualFold(row.text(name), value) {
			return importError("SQL self recipient", signal.ErrInvalidAccountImport)
		}
	}

	return nil
}

func validateGroupRow(row sourceRow) error {
	err := sourceBooleans(row, "blocked", "profile_sharing", "permission_denied")
	if err != nil {
		return err
	}

	if row.integer("blocked") != 0 || row.integer("profile_sharing") != 1 {
		return importError("group state", signal.ErrUnsupportedAccountImport)
	}

	for name, length := range map[string]int{
		"group_id": sourceGroupIDBytes, "master_key": sourceGroupKeyBytes, "distribution_id": sourceUUIDBytes,
	} {
		if len(row.blob(name)) != length {
			return importError("SQL group fields", signal.ErrInvalidAccountImport)
		}
	}

	if !row.null("storage_id") && len(row.blob("storage_id")) != 16 {
		return importError("SQL group storage ID", signal.ErrInvalidAccountImport)
	}

	return nil
}
