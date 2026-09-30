//go:build cgo || libsignal_go

//nolint:paralleltest // HTTP transport replacement requires serial parent and child tests.
package signal_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	mstore "github.com/cwbudde/mautrix-signal/pkg/signalmeow/store"
	"github.com/google/uuid"
)

func validRawProfile(t *testing.T) signal.RawOwnProfile {
	t.Helper()

	name := make([]byte, 53)
	copy(name, "Given Name\x00Family Name")

	about := make([]byte, 128)
	copy(about, "About")

	emoji := make([]byte, 32)
	copy(emoji, "🌊")

	return signal.RawOwnProfile{
		ACI: seededACI, Name: independentProfileCipher(t, name), About: independentProfileCipher(t, about),
		AboutEmoji: independentProfileCipher(t, emoji), Credential: make([]byte, 497), Capabilities: map[string]bool{},
		Avatar: "avatar/path", PaymentAddress: independentProfileCipher(t, make([]byte, 554)),
		PhoneNumberSharing: independentProfileCipher(t, []byte{1}),
		Badges:             json.RawMessage(`[{"id":"badge","visible":true}]`),
	}
}

func TestDecodeOwnProfilePreflight(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		modify func(*signal.RawOwnProfile)
		want   error
	}{
		{"credential missing", func(r *signal.RawOwnProfile) { r.Credential = nil }, signal.ErrProfileKeyChanged},
		{"credential short", func(r *signal.RawOwnProfile) { r.Credential = make([]byte, 496) }, signal.ErrInvalidProfile},
		{"wrong account", func(r *signal.RawOwnProfile) { r.ACI = aliceUser }, signal.ErrInvalidProfile},
		{"v2", func(r *signal.RawOwnProfile) { r.Capabilities["profiles_v2"] = true }, signal.ErrProfileV2Unsupported},
		{"missing capabilities", func(r *signal.RawOwnProfile) { r.Capabilities = nil }, signal.ErrInvalidProfile},
		{"missing name", func(r *signal.RawOwnProfile) { r.Name = nil }, signal.ErrInvalidProfile},
		{"short cipher", func(r *signal.RawOwnProfile) { r.About = []byte{1, 2} }, signal.ErrInvalidProfile},
		{"multiple separators", func(r *signal.RawOwnProfile) {
			p := make([]byte, 53)
			copy(p, "a\x00b\x00c")
			r.Name = independentProfileCipher(t, p)
		}, signal.ErrInvalidProfile},
		{"invalid utf8", func(r *signal.RawOwnProfile) {
			p := make([]byte, 53)
			p[0] = 255
			r.Name = independentProfileCipher(t, p)
		}, signal.ErrInvalidProfile},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			raw := validRawProfile(t)
			testCase.modify(&raw)

			_, err := signal.DecodeOwnProfile(raw, seededACI, profileTestKey())
			if !errors.Is(err, testCase.want) {
				t.Fatalf("error=%v want %v", err, testCase.want)
			}
		})
	}

	for _, body := range []string{
		`{"capabilities":{"x":null}}`, `{"capabilities":{"x":"true"}}`, `{"name":"!!!"}`, `{} {}`,
	} {
		var raw signal.RawOwnProfile
		if json.Unmarshal([]byte(body), &raw) == nil {
			t.Fatalf("invalid raw accepted: %s", body)
		}
	}
}

func TestPrepareOwnProfilePreservation(t *testing.T) { //nolint:cyclop,funlen // checks every preserved wire field
	t.Parallel()
	raw := validRawProfile(t)

	current, err := signal.DecodeOwnProfile(raw, seededACI, profileTestKey())
	if err != nil {
		t.Fatal(err)
	}

	if current.GivenName != "Given Name" || current.FamilyName != "Family Name" {
		t.Fatalf("name=%+v", current)
	}

	req, _, changed, err := signal.PrepareOwnProfileUpdate(
		raw, current, profileTestKey(), signal.ProfileUpdate{About: new("About")})
	if err != nil || changed {
		t.Fatalf("no-op=%v,%v", changed, err)
	}

	if !bytes.Equal(req.Name, raw.Name) {
		t.Fatal("no-op encrypted name")
	}

	req, proposed, changed, err := signal.PrepareOwnProfileUpdate(
		raw, current, profileTestKey(), signal.ProfileUpdate{About: new("")})
	if err != nil || !changed || proposed.About != "" {
		t.Fatalf("clear=%+v,%v", proposed, err)
	}

	if req.About == nil ||
		len(req.About) != 0 ||
		!bytes.Equal(req.Name, raw.Name) ||
		!bytes.Equal(req.AboutEmoji, raw.AboutEmoji) ||
		!bytes.Equal(req.PaymentAddress, raw.PaymentAddress) ||
		!bytes.Equal(req.PhoneNumberSharing, raw.PhoneNumberSharing) ||
		!req.Avatar ||
		!req.SameAvatar {
		t.Fatal("snapshot preservation failed")
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Contains(data, []byte("badgeIds")) {
		t.Fatal("badgeIds included")
	}

	raw.PaymentAddress = nil
	raw.PhoneNumberSharing = []byte{}

	req, _, _, err = signal.PrepareOwnProfileUpdate(
		raw, current, profileTestKey(), signal.ProfileUpdate{About: new("new")})
	if err != nil {
		t.Fatal(err)
	}

	data, err = json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Contains(data, []byte(`"paymentAddress":null`)) ||
		!bytes.Contains(data, []byte(`"phoneNumberSharing":""`)) {
		t.Fatalf("null/empty lost: %s", data)
	}
}

type profileRoundTrip func(*http.Request) (*http.Response, error)

func (f profileRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type profileBrokenBody struct{}

func (profileBrokenBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (profileBrokenBody) Close() error             { return nil }
func profileHTTPDevice() *mstore.DeviceData {
	return &mstore.DeviceData{ACI: uuid.MustParse(seededACI), DeviceID: 2, Password: "profile-test-password"}
}

func profileHTTPResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestProfileHTTPRequestOnce(t *testing.T) { //nolint:cyclop,paralleltest // replaces shared transport
	for _, status := range []int{0, 401, 403, 412, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0

			t.Cleanup(signal.SetSignalTransport(profileRoundTrip(func(request *http.Request) (*http.Response, error) {
				calls++

				user, pass, ok := request.BasicAuth()
				if !ok ||
					user != seededACI+".2" ||
					pass != "profile-test-password" ||
					request.URL.Path != "/v1/profile" ||
					request.Method != http.MethodPut ||
					request.Header.Get("User-Agent") == "" ||
					request.Header.Get("X-Signal-Agent") == "" ||
					request.Header.Get("Content-Type") != "application/json" {
					t.Errorf("request headers/path incorrect")
				}

				if status == 0 {
					return nil, io.ErrUnexpectedEOF
				}

				return profileHTTPResponse(status, "{}"), nil
			})))

			_, accepted, err := signal.ProfileHTTPRequest(
				t.Context(), profileHTTPDevice(), http.MethodPut, "/v1/profile", []byte(`{}`))
			if err == nil || accepted || calls != 1 {
				t.Fatalf("accepted/calls/error=%v/%d/%v", accepted, calls, err)
			}

			want := map[int]error{
				401: signal.ErrDeviceUnlinked, 403: signal.ErrProfileRejected, 412: signal.ErrProfileV2Unsupported,
			}[status]
			if want != nil && !errors.Is(err, want) {
				t.Fatalf("error=%v want=%v", err, want)
			}
		})
	}
}

func TestProfileHTTPRequestAcceptance(t *testing.T) { //nolint:paralleltest // replaces shared transport
	t.Cleanup(signal.SetSignalTransport(profileRoundTrip(func(*http.Request) (*http.Response, error) {
		r := profileHTTPResponse(200, "")
		r.Body = profileBrokenBody{}

		return r, nil
	})))

	_, accepted, err := signal.ProfileHTTPRequest(t.Context(), profileHTTPDevice(), http.MethodPut, "/v1/profile", nil)
	if !accepted || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("accepted=%v error=%v", accepted, err)
	}

	_, accepted, err = signal.ProfileHTTPRequest(t.Context(), profileHTTPDevice(), http.MethodGet, "/v1/profile", nil)
	if accepted || err == nil {
		t.Fatalf("GET accepted=%v error=%v", accepted, err)
	}
}

func TestProfileHTTPRequestLimits(t *testing.T) { //nolint:paralleltest // replaces shared transport
	for _, body := range []string{strings.Repeat(" ", 1<<20) + "{}", `{} {}`, `not JSON`} {
		t.Run("invalid", func(t *testing.T) {
			t.Cleanup(signal.SetSignalTransport(profileRoundTrip(func(*http.Request) (*http.Response, error) {
				return profileHTTPResponse(200, body), nil
			})))

			_, accepted, err := signal.ProfileHTTPRequest(t.Context(), profileHTTPDevice(), http.MethodPut, "/v1/profile", nil)
			if err == nil || !accepted {
				t.Fatalf("accepted=%v error=%v", accepted, err)
			}
		})
	}
}

func TestProfileHTTPRequestRejectsRedirect(t *testing.T) { //nolint:paralleltest // replaces shared transport
	for _, status := range []int{301, 302, 307, 308} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0

			t.Cleanup(signal.SetSignalTransport(profileRoundTrip(func(*http.Request) (*http.Response, error) {
				calls++
				r := profileHTTPResponse(status, "")
				r.Header.Set("Location", "https://other.example/profile")

				return r, nil
			})))

			_, accepted, err := signal.ProfileHTTPRequest(
				t.Context(), profileHTTPDevice(), http.MethodPut, "/v1/profile", []byte(`{}`))
			if err == nil || accepted || calls != 1 {
				t.Fatalf("accepted=%v calls=%d error=%v", accepted, calls, err)
			}
		})
	}
}

func TestProfileHTTPRequestDoesNotExposeCredentialPath(t *testing.T) { //nolint:paralleltest // shared HTTP transport
	t.Cleanup(signal.SetSignalTransport(profileRoundTrip(func(*http.Request) (*http.Response, error) {
		return nil, io.ErrUnexpectedEOF
	})))

	_, _, err := signal.ProfileHTTPRequest(
		t.Context(), profileHTTPDevice(), http.MethodGet, "/v1/profile/sensitive-credential-request", nil)
	if err == nil ||
		strings.Contains(err.Error(), "sensitive-credential-request") ||
		!errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("unsafe or unwrapped request error: %v", err)
	}
}

func TestProfileHTTPRequestUncertainWrite(t *testing.T) { //nolint:paralleltest // shared HTTP transport
	for _, status := range []int{0, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Cleanup(signal.SetSignalTransport(profileRoundTrip(func(*http.Request) (*http.Response, error) {
				if status == 0 {
					return nil, io.ErrUnexpectedEOF
				}

				return profileHTTPResponse(status, ""), nil
			})))

			_, accepted, err := signal.ProfileHTTPRequest(t.Context(), profileHTTPDevice(), http.MethodPut, "/v1/profile", nil)
			if accepted || err == nil || !strings.Contains(err.Error(), "outcome unknown") ||
				!strings.Contains(err.Error(), "profile show") {
				t.Fatalf("uncertain write hint=%v accepted=%v", err, accepted)
			}

			if errors.Is(err, signal.ErrProfileRejected) {
				t.Fatalf("uncertain write misclassified as rejected: %v", err)
			}
		})
	}
}
