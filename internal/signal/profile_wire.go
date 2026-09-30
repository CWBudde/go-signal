//go:build cgo || libsignal_go

package signal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/cwbudde/mautrix-signal/pkg/libsignalgo"
	mstore "github.com/cwbudde/mautrix-signal/pkg/signalmeow/store"
	"github.com/cwbudde/mautrix-signal/pkg/signalmeow/web"
	"github.com/google/uuid"
)

type rawOwnProfile struct {
	ACI                string          `json:"uuid"`
	Name               []byte          `json:"name"`
	About              []byte          `json:"about"`
	AboutEmoji         []byte          `json:"aboutEmoji"`
	PaymentAddress     []byte          `json:"paymentAddress"`
	PhoneNumberSharing []byte          `json:"phoneNumberSharing"`
	Credential         []byte          `json:"credential"`
	Avatar             string          `json:"avatar"`
	Badges             json.RawMessage `json:"badges"`
	Capabilities       map[string]bool `json:"capabilities"`
}

func (raw *rawOwnProfile) UnmarshalJSON(data []byte) error {
	type plain rawOwnProfile

	var decoded plain

	err := json.Unmarshal(data, &decoded)
	if err != nil {
		return fmt.Errorf("%w: malformed profile JSON", ErrInvalidProfile)
	}

	var fields struct {
		Capabilities map[string]json.RawMessage `json:"capabilities"`
	}

	err = json.Unmarshal(data, &fields)
	if err != nil {
		return fmt.Errorf("%w: malformed capabilities", ErrInvalidProfile)
	}

	if fields.Capabilities == nil {
		return fmt.Errorf("%w: missing capabilities", ErrInvalidProfile)
	}

	for _, value := range fields.Capabilities {
		value = bytes.TrimSpace(value)
		if !bytes.Equal(value, []byte("true")) && !bytes.Equal(value, []byte("false")) {
			return fmt.Errorf("%w: malformed capability value", ErrInvalidProfile)
		}
	}

	*raw = rawOwnProfile(decoded)

	return nil
}

type profileWriteRequest struct {
	Commitment         []byte `json:"commitment"`
	Version            string `json:"version"`
	Name               []byte `json:"name"`
	About              []byte `json:"about"`
	AboutEmoji         []byte `json:"aboutEmoji"`
	PaymentAddress     []byte `json:"paymentAddress"`
	PhoneNumberSharing []byte `json:"phoneNumberSharing"`
	Avatar             bool   `json:"avatar"`
	SameAvatar         bool   `json:"sameAvatar"`
}

func decodeOwnProfile(raw rawOwnProfile, ownACI string, key libsignalgo.ProfileKey) (Profile, error) {
	err := checkRawProfile(raw, ownACI)
	if err != nil {
		return Profile{}, err
	}

	name, err := decryptProfileText(key, raw.Name, []int{53, 257})
	if err != nil {
		return Profile{}, err
	}

	if !utf8.ValidString(name) || strings.Count(name, "\x00") > 1 {
		return Profile{}, fmt.Errorf("%w: name encoding", ErrInvalidProfile)
	}

	given, family, _ := strings.Cut(name, "\x00")

	profile := Profile{ACI: ownACI, GivenName: given, FamilyName: family, AvatarPath: raw.Avatar}

	err = decodeOptionalProfileText(raw, key, &profile)
	if err != nil {
		return Profile{}, err
	}

	err = checkOpaqueProfile(raw, key)
	if err != nil {
		return Profile{}, err
	}

	return profile, nil
}

func decodeOptionalProfileText(raw rawOwnProfile, key libsignalgo.ProfileKey, profile *Profile) error {
	for _, field := range []struct {
		encrypted []byte
		sizes     []int
		target    *string
	}{
		{raw.About, []int{128, 254, 512}, &profile.About}, {raw.AboutEmoji, []int{32}, &profile.AboutEmoji},
	} {
		if len(field.encrypted) == 0 {
			continue
		}

		text, decodeErr := decryptProfileText(key, field.encrypted, field.sizes)
		if decodeErr != nil {
			return decodeErr
		}

		if !utf8.ValidString(text) || strings.ContainsRune(text, '\x00') {
			return fmt.Errorf("%w: text encoding", ErrInvalidProfile)
		}

		*field.target = text
	}

	return nil
}

func checkOpaqueProfile(raw rawOwnProfile, key libsignalgo.ProfileKey) error {
	for _, field := range []struct {
		encrypted []byte
		size      int
	}{{raw.PaymentAddress, 554}, {raw.PhoneNumberSharing, 1}} {
		if len(field.encrypted) > 0 {
			_, err := decryptProfileBytes(key, field.encrypted, []int{field.size})
			if err != nil {
				return err
			}
		}
	}

	if len(raw.Badges) > 0 && !json.Valid(raw.Badges) {
		return fmt.Errorf("%w: badges", ErrInvalidProfile)
	}

	return nil
}

func checkRawProfile(raw rawOwnProfile, ownACI string) error {
	switch {
	case raw.ACI != ownACI:
		return fmt.Errorf("%w: account identifier mismatch", ErrInvalidProfile)
	case raw.Capabilities == nil:
		return fmt.Errorf("%w: missing capabilities", ErrInvalidProfile)
	case raw.Capabilities["profiles_v2"]:
		return ErrProfileV2Unsupported
	case len(raw.Credential) == 0:
		return fmt.Errorf("%w: server did not confirm current profile version", ErrProfileKeyChanged)
	case len(raw.Credential) != profileCredentialSize:
		return fmt.Errorf("%w: credential size", ErrInvalidProfile)
	}

	_, err := uuid.Parse(ownACI)
	if err != nil {
		return fmt.Errorf("%w: account identifier", ErrInvalidProfile)
	}

	return nil
}

func prepareOwnProfileUpdate(
	raw rawOwnProfile, current Profile, key libsignalgo.ProfileKey, update ProfileUpdate,
) (profileWriteRequest, Profile, bool, error) {
	request := profileWriteRequest{
		Name: bytes.Clone(raw.Name), About: bytes.Clone(raw.About), AboutEmoji: bytes.Clone(raw.AboutEmoji),
		PaymentAddress: bytes.Clone(raw.PaymentAddress), PhoneNumberSharing: bytes.Clone(raw.PhoneNumberSharing),
		Avatar: true, SameAvatar: true,
	}

	proposed, changed, err := update.Apply(current)
	if err != nil {
		return profileWriteRequest{}, Profile{}, false, err
	}

	if !changed {
		return request, current, false, nil
	}

	aci, err := uuid.Parse(current.ACI)
	if err != nil {
		return profileWriteRequest{}, Profile{}, false, fmt.Errorf("%w: account identifier", ErrInvalidProfile)
	}

	commitment, err := key.GetCommitment(aci)
	if err != nil {
		return profileWriteRequest{}, Profile{}, false, fmt.Errorf("profile commitment: %w", err)
	}

	version, err := key.GetProfileKeyVersion(aci)
	if err != nil {
		return profileWriteRequest{}, Profile{}, false, fmt.Errorf("profile version: %w", err)
	}

	request.Commitment = bytes.Clone(commitment[:])
	request.Version = version.String()

	err = prepareProfileName(current, proposed, key, &request)
	if err != nil {
		return profileWriteRequest{}, Profile{}, false, err
	}

	err = prepareOptionalProfileText(current, proposed, key, &request)
	if err != nil {
		return profileWriteRequest{}, Profile{}, false, err
	}

	return request, proposed, true, nil
}

func prepareProfileName(current, proposed Profile, key libsignalgo.ProfileKey, request *profileWriteRequest) error {
	var err error

	if proposed.GivenName != current.GivenName || proposed.FamilyName != current.FamilyName {
		name := proposed.GivenName
		if proposed.FamilyName != "" {
			name += "\x00" + proposed.FamilyName
		}

		request.Name, err = encryptProfileText(key, name, []int{53, 257})
		if err != nil {
			return err
		}
	}

	return nil
}

func prepareOptionalProfileText(
	current, proposed Profile, key libsignalgo.ProfileKey, request *profileWriteRequest,
) error {
	var err error

	for _, field := range []struct {
		old, value string
		sizes      []int
		target     *[]byte
	}{
		{current.About, proposed.About, []int{128, 254, 512}, &request.About},
		{current.AboutEmoji, proposed.AboutEmoji, []int{32}, &request.AboutEmoji},
	} {
		if field.old == field.value {
			continue
		}

		if field.value == "" {
			*field.target = []byte{}
			continue
		}

		*field.target, err = encryptProfileText(key, field.value, field.sizes)
		if err != nil {
			return err
		}
	}

	return nil
}

const (
	maxProfileResponse    = 1 << 20
	profileCredentialSize = 497
)

// profileHTTPRequest sends once, preserving acceptance even when reading the reply fails.
func profileHTTPRequest(
	ctx context.Context, device *mstore.DeviceData, method, path string, body []byte,
) ([]byte, bool, error) {
	request, err := http.NewRequestWithContext(ctx, method, "https://"+web.APIHostname+path, bytes.NewReader(body))
	if err != nil {
		return nil, false, fmt.Errorf("build profile request: %w", err)
	}

	username, password := device.BasicAuthCreds()
	request.SetBasicAuth(username, password)
	request.Header.Set("Content-Type", string(web.ContentTypeJSON))
	request.Header.Set("User-Agent", web.UserAgent)
	request.Header.Set("X-Signal-Agent", web.SignalAgent)

	client := *web.SignalHTTPClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	response, err := client.Do(request)
	if err != nil {
		if requestErr, ok := errors.AsType[*url.Error](err); ok {
			err = requestErr.Err
		}

		return nil, false, profileTransportError(method, err)
	}

	defer func() { _ = response.Body.Close() }()

	accepted := method == http.MethodPut &&
		response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices

	err = profileResponseStatus(method, response.StatusCode)
	if err != nil {
		return nil, false, err
	}

	data, err := readProfileResponse(response.Body)

	return data, accepted, err
}

func readProfileResponse(body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxProfileResponse+1))
	if err != nil {
		return nil, fmt.Errorf("read profile response: %w", err)
	}

	if len(data) > maxProfileResponse {
		return nil, fmt.Errorf("%w: response exceeds 1 MiB", ErrInvalidProfile)
	}

	if len(bytes.TrimSpace(data)) > 0 && !json.Valid(data) {
		return nil, fmt.Errorf("%w: invalid response JSON", ErrInvalidProfile)
	}

	return data, nil
}

func profileResponseStatus(method string, status int) error {
	switch {
	case status == http.StatusUnauthorized || method == http.MethodGet && status == http.StatusForbidden:
		return ErrDeviceUnlinked
	case status == http.StatusPreconditionFailed:
		return ErrProfileV2Unsupported
	case method == http.MethodPut && status >= http.StatusInternalServerError:
		return fmt.Errorf("%w: profile write outcome unknown (HTTP %d); run profile show before retrying",
			errServerStatus, status)
	case status < http.StatusOK || status >= http.StatusMultipleChoices:
		if method == http.MethodPut {
			return fmt.Errorf("%w: HTTP %d", ErrProfileRejected, status)
		}

		return fmt.Errorf("%w: profile read HTTP %d", errServerStatus, status)
	}

	return nil
}

func profileTransportError(method string, err error) error {
	if method == http.MethodPut {
		return fmt.Errorf("profile write outcome unknown; run profile show before retrying: %w", err)
	}

	return fmt.Errorf("profile request failed: %w", err)
}
