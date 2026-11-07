//go:build cgo || libsignal_go

package accountimport

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/google/uuid"
)

// These are the v0.14.9 Storage records, not signal-cli's mutating loader.
// Pointers distinguish required values from omitted/null JSON members.
type sourceRegistry struct {
	Version  *int             `json:"version"`
	Accounts *[]registryEntry `json:"accounts"`
}

type registryEntry struct {
	Path        *string `json:"path"`
	Environment *string `json:"environment"`
	Number      *string `json:"number"`
	UUID        *string `json:"uuid"`
}

type sourceAccount struct {
	Version              *int            `json:"version"`
	Timestamp            *int64          `json:"timestamp"`
	ServiceEnvironment   *string         `json:"serviceEnvironment"`
	Registered           *bool           `json:"registered"`
	Number               *string         `json:"number"`
	Username             *string         `json:"username"`
	EncryptedDeviceName  *string         `json:"encryptedDeviceName"`
	DeviceID             *uint32         `json:"deviceId"`
	IsMultiDevice        *bool           `json:"isMultiDevice"`
	Password             *string         `json:"password"`
	ACI                  *sourceIdentity `json:"aciAccountData"`
	PNI                  *sourceIdentity `json:"pniAccountData"`
	RegistrationLockPIN  *string         `json:"registrationLockPin"`
	PINMasterKey         *string         `json:"pinMasterKey"`
	StorageKey           *string         `json:"storageKey"`
	AccountEntropyPool   *string         `json:"accountEntropyPool"`
	AuthCredentialSalt   *string         `json:"authCredentialSalt"`
	MediaRootBackupKey   *string         `json:"mediaRootBackupKey"`
	ProfileKey           *string         `json:"profileKey"`
	UsernameLinkEntropy  *string         `json:"usernameLinkEntropy"`
	UsernameLinkServerID *string         `json:"usernameLinkServerId"`
}

type sourceIdentity struct {
	ServiceID                  *string         `json:"serviceId"`
	RegistrationID             *uint32         `json:"registrationId"`
	IdentityPrivateKey         *string         `json:"identityPrivateKey"`
	IdentityPublicKey          *string         `json:"identityPublicKey"`
	NextPreKeyID               *int64          `json:"nextPreKeyId"`
	NextSignedPreKeyID         *int64          `json:"nextSignedPreKeyId"`
	ActiveSignedPreKeyID       sourceReference `json:"activeSignedPreKeyId"`
	NextKyberPreKeyID          *int64          `json:"nextKyberPreKeyId"`
	ActiveLastResortKyberKeyID sourceReference `json:"activeLastResortKyberPreKeyId"`
}

type sourceReference struct {
	value *int64
}

func (reference *sourceReference) UnmarshalJSON(raw []byte) error {
	var value *int64

	err := json.Unmarshal(raw, &value)
	if err != nil || value == nil {
		return signal.ErrInvalidAccountImport
	}

	reference.value = value

	return nil
}

const (
	sourceJSONDepth       = 64
	sourceRegistryVersion = 2
	sourceLiveEnvironment = "LIVE"
	sourceAccountVersion  = 11
	sourcePrivateKeyBytes = 32
	sourcePublicKeyBytes  = 33
)

var sourceE164 = regexp.MustCompile(`^\+[1-9][0-9]{1,14}$`)

func importError(category string, cause error) error {
	// Category strings are fixed by the caller. Parser/OS diagnostics can include
	// source paths, keys or values and must never reach the user.
	return fmt.Errorf("account import %s: %w", category, cause)
}

func strictJSON(raw []byte, target any) error {
	if !utf8.Valid(raw) {
		return importError("JSON encoding", signal.ErrInvalidAccountImport)
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	err := uniqueJSONValue(decoder, 0)
	if err != nil {
		return importError("JSON structure", signal.ErrInvalidAccountImport)
	}

	_, err = decoder.Token()
	if !errors.Is(err, io.EOF) {
		return importError("trailing JSON", signal.ErrInvalidAccountImport)
	}

	err = exactJSONFields(raw, reflect.TypeOf(target))
	if err != nil {
		return err
	}

	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	err = decoder.Decode(target)
	if err != nil {
		if strings.HasPrefix(err.Error(), "json: unknown field ") {
			return importError("JSON fields", signal.ErrUnsupportedAccountImport)
		}

		return importError("JSON types", signal.ErrInvalidAccountImport)
	}

	return nil
}

// encoding/json matches field names case-insensitively even with
// DisallowUnknownFields. Check the exact v0.14.9 tags before typed decoding.
func exactJSONFields(raw []byte, schema reflect.Type) error {
	for schema.Kind() == reflect.Pointer {
		schema = schema.Elem()
	}

	if reflect.PointerTo(schema).Implements(reflect.TypeFor[json.Unmarshaler]()) {
		return nil
	}

	if schema.Kind() == reflect.Struct {
		return exactJSONObject(raw, schema)
	}

	if schema.Kind() != reflect.Slice {
		return nil
	}

	var values []json.RawMessage

	decodeErr := json.Unmarshal(raw, &values)
	if decodeErr != nil {
		return importError("JSON array", signal.ErrInvalidAccountImport)
	}

	for _, value := range values {
		fieldErr := exactJSONFields(value, schema.Elem())
		if fieldErr != nil {
			return fieldErr
		}
	}

	return nil
}

func exactJSONObject(raw []byte, schema reflect.Type) error {
	var fields map[string]json.RawMessage

	decodeErr := json.Unmarshal(raw, &fields)
	if decodeErr != nil {
		return importError("JSON object", signal.ErrInvalidAccountImport)
	}

	allowed := make(map[string]reflect.Type, schema.NumField())
	for field := range schema.Fields() {
		allowed[field.Tag.Get("json")] = field.Type
	}

	for key, value := range fields {
		child, known := allowed[key]
		if !known {
			return importError("JSON fields", signal.ErrUnsupportedAccountImport)
		}

		fieldErr := exactJSONFields(value, child)
		if fieldErr != nil {
			return fieldErr
		}
	}

	return nil
}

func uniqueJSONValue(decoder *json.Decoder, depth int) error {
	if depth > sourceJSONDepth {
		return signal.ErrInvalidAccountImport
	}

	token, err := decoder.Token()
	if err != nil {
		return signal.ErrInvalidAccountImport
	}

	delim, container := token.(json.Delim)
	if !container {
		return nil
	}

	if delim == '{' {
		return uniqueJSONObject(decoder, depth)
	}

	for decoder.More() {
		err = uniqueJSONValue(decoder, depth+1)
		if err != nil {
			return err
		}
	}

	return closeJSONContainer(decoder, ']')
}

func uniqueJSONObject(decoder *json.Decoder, depth int) error {
	seen := make(map[string]bool)

	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return signal.ErrInvalidAccountImport
		}

		name, isString := key.(string)
		if !isString || seen[name] {
			return signal.ErrInvalidAccountImport
		}

		seen[name] = true

		err = uniqueJSONValue(decoder, depth+1)
		if err != nil {
			return err
		}
	}

	return closeJSONContainer(decoder, '}')
}

func closeJSONContainer(decoder *json.Decoder, want json.Delim) error {
	closing, err := decoder.Token()
	if err != nil || closing != want {
		return signal.ErrInvalidAccountImport
	}

	return nil
}

func sourceUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && strings.EqualFold(value, id.String())
}

func sourceBasename(value string) bool {
	return value != "" && value != "." && value != ".." && value != "accounts.json" &&
		!strings.ContainsAny(value, "/\\\x00")
}

func selectSource(registry sourceRegistry, selector string) (registryEntry, error) {
	selectionErr := validateRegistrySelection(registry, selector)
	if selectionErr != nil {
		return registryEntry{}, selectionErr
	}

	eligible := eligibleSourceEntries(*registry.Accounts)

	var selected []registryEntry

	for _, entry := range eligible {
		if selector == "" || selector == *entry.Number || strings.EqualFold(selector, *entry.UUID) {
			selected = append(selected, entry)
		}
	}

	if len(selected) == 0 {
		return registryEntry{}, importError("source selection", signal.ErrInvalidAccountImport)
	}

	if len(selected) != 1 {
		return registryEntry{}, importError("source selection", signal.ErrAmbiguousAccountImport)
	}

	duplicateErr := checkSourceDuplicates(*registry.Accounts, selected[0])
	if duplicateErr != nil {
		return registryEntry{}, duplicateErr
	}

	return selected[0], nil
}

func validateRegistrySelection(registry sourceRegistry, selector string) error {
	if registry.Version == nil || registry.Accounts == nil {
		return importError("registry fields", signal.ErrInvalidAccountImport)
	}

	if *registry.Version != sourceRegistryVersion {
		return importError("registry version", signal.ErrUnsupportedAccountImport)
	}

	if selector != "" && !sourceE164.MatchString(selector) && !sourceUUID(selector) {
		return importError("source selector", signal.ErrInvalidAccountImport)
	}

	return nil
}

func checkSourceDuplicates(entries []registryEntry, chosen registryEntry) error {
	for _, field := range []struct {
		get  func(registryEntry) *string
		fold bool
	}{
		{func(entry registryEntry) *string { return entry.Number }, false},
		{func(entry registryEntry) *string { return entry.UUID }, true},
		{func(entry registryEntry) *string { return entry.Path }, false},
	} {
		matches := 0

		for _, entry := range entries {
			if entry.Environment == nil || *entry.Environment != sourceLiveEnvironment {
				continue
			}

			if sameRegistryField(field.get(entry), field.get(chosen), field.fold) {
				matches++
			}
		}

		if matches != 1 {
			return importError("duplicate source account", signal.ErrAmbiguousAccountImport)
		}
	}

	return nil
}

func sameRegistryField(left, right *string, fold bool) bool {
	if left == nil || right == nil {
		return false
	}

	if fold {
		return strings.EqualFold(*left, *right)
	}

	return *left == *right
}

func validateSourceAccount(account *sourceAccount, entry registryEntry) error {
	if slices.Contains([]bool{
		account.Version == nil, account.Timestamp == nil, account.ServiceEnvironment == nil,
		account.Registered == nil, account.Number == nil, account.DeviceID == nil,
		account.IsMultiDevice == nil, account.Password == nil, account.ProfileKey == nil,
	}, true) {
		return importError("account fields", signal.ErrInvalidAccountImport)
	}

	eligibilityErr := validateSourceEligibility(account)
	if eligibilityErr != nil {
		return eligibilityErr
	}

	if *account.Number != *entry.Number || !validSourceKey(*account.ProfileKey, sourcePrivateKeyBytes) {
		return importError("account identity", signal.ErrInvalidAccountImport)
	}

	err := validateSourceIdentity(account.ACI, "")
	if err != nil {
		return err
	}

	err = validateSourceIdentity(account.PNI, "PNI:")
	if err != nil {
		return err
	}

	if !strings.EqualFold(*account.ACI.ServiceID, *entry.UUID) {
		return importError("registry identity mismatch", signal.ErrInvalidAccountImport)
	}

	return nil
}

func validateSourceIdentity(identity *sourceIdentity, prefix string) error {
	if identity == nil {
		return importError("local identity fields", signal.ErrInvalidAccountImport)
	}

	if slices.Contains([]bool{
		identity.ServiceID == nil, identity.RegistrationID == nil,
		identity.IdentityPrivateKey == nil, identity.IdentityPublicKey == nil,
		identity.NextPreKeyID == nil, identity.NextSignedPreKeyID == nil, identity.NextKyberPreKeyID == nil,
	}, true) {
		return importError("local identity fields", signal.ErrInvalidAccountImport)
	}

	if *identity.RegistrationID == 0 {
		return importError("local registration ID", signal.ErrInvalidAccountImport)
	}

	value, hasPrefix := strings.CutPrefix(*identity.ServiceID, prefix)
	if !hasPrefix || !sourceUUID(value) ||
		!validSourceKey(*identity.IdentityPrivateKey, sourcePrivateKeyBytes) ||
		!validSourceKey(*identity.IdentityPublicKey, sourcePublicKeyBytes) {
		return importError("local identity encoding", signal.ErrInvalidAccountImport)
	}

	// Pair agreement, retained-record bindings and allocator validity are Task 4.
	return nil
}

func validSourceKey(value string, length int) bool {
	decoded, err := base64.StdEncoding.Strict().DecodeString(value)
	return err == nil && len(decoded) == length && base64.StdEncoding.EncodeToString(decoded) == value
}

func eligibleSourceEntries(entries []registryEntry) []registryEntry {
	var eligible []registryEntry

	for _, entry := range entries {
		if !sourceEntryEligible(entry) {
			continue
		}

		eligible = append(eligible, entry)
	}

	return eligible
}

func sourceEntryEligible(entry registryEntry) bool {
	if slices.Contains([]bool{entry.Environment == nil, entry.Number == nil, entry.UUID == nil, entry.Path == nil}, true) {
		return false
	}

	return *entry.Environment == sourceLiveEnvironment && sourceE164.MatchString(*entry.Number) &&
		sourceBasename(*entry.Path) && sourceUUID(*entry.UUID)
}

func validateSourceEligibility(account *sourceAccount) error {
	if *account.Password == "" || *account.Timestamp < 0 {
		return importError("account values", signal.ErrInvalidAccountImport)
	}

	if *account.Version != sourceAccountVersion || *account.ServiceEnvironment != sourceLiveEnvironment ||
		!*account.Registered || *account.DeviceID <= 1 {
		return importError("account eligibility", signal.ErrUnsupportedAccountImport)
	}

	return nil
}
