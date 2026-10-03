//nolint:lll // Behavioral matrices keep independent fixtures and assertions together.
package signal_test

import (
	"encoding/base64"
	"errors"
	"io"
	"net/url"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
	"google.golang.org/protobuf/encoding/protowire"
)

func joinFragment(keyLen, passwordLen int) string {
	contents := protowire.AppendTag(nil, 1, protowire.BytesType)
	contents = protowire.AppendBytes(contents, []byte(strings.Repeat("K", keyLen)))
	contents = protowire.AppendTag(contents, 2, protowire.BytesType)
	contents = protowire.AppendBytes(contents, []byte(strings.Repeat("P", passwordLen)))
	raw := protowire.AppendTag(nil, 1, protowire.BytesType)
	raw = protowire.AppendBytes(raw, contents)

	return base64.RawURLEncoding.EncodeToString(raw)
}

//nolint:funlen // Complete URL and protobuf rejection matrix.
func TestGroupJoinLinkValidation(t *testing.T) {
	t.Parallel()

	fragment := joinFragment(32, 16)

	raw, err := base64.RawURLEncoding.DecodeString(fragment)
	if err != nil {
		t.Fatal(err)
	}

	contents := protowire.AppendTag(nil, 2, protowire.BytesType)
	contents = protowire.AppendBytes(contents, []byte(strings.Repeat("P", 16)))
	contents = protowire.AppendTag(contents, 1, protowire.BytesType)
	contents = protowire.AppendBytes(contents, []byte(strings.Repeat("K", 32)))
	reversed := protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), contents)
	duplicateKey := append(append([]byte(nil), contents...), protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), []byte(strings.Repeat("K", 32)))...)

	cases := []struct {
		name, input string
		valid       bool
	}{
		{"reversed fields", "https://signal.group/#" + base64.RawURLEncoding.EncodeToString(reversed), true},
		{"duplicate key", "https://signal.group/#" + base64.RawURLEncoding.EncodeToString(protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), duplicateKey)), false},
		{"https", "https://signal.group/#" + fragment, true},
		{"sgnl empty path", "sgnl://signal.group#" + fragment, true},
		{"case and trim", " \tHTTPS://SIGNAL.GROUP/#" + fragment + "\n", true},
		{"padded", "https://signal.group/#" + base64.URLEncoding.EncodeToString(raw), true},
		{"host", "https://other.group/#" + fragment, false},
		{"host suffix", "https://signal.group.evil/#" + fragment, false},
		{"credentials", "https://secret@signal.group/#" + fragment, false},
		{"port", "https://signal.group:443/#" + fragment, false},
		{"query", "https://signal.group/?secret#" + fragment, false},
		{"empty query", "https://signal.group/?#" + fragment, false},
		{"path", "https://signal.group/join#" + fragment, false},
		{"encoded path", "https://signal.group/%2f#" + fragment, false},
		{"scheme", "http://signal.group/#" + fragment, false},
		{"internal space", "https://signal.group/# " + fragment, false},
		{"internal newline", "https://signal.group/#" + fragment + "\nX", false},
		{"limit before trim", strings.Repeat(" ", 4097) + "https://signal.group/#" + fragment, false},
		{"no fragment", "https://signal.group/", false},
		{"bad base64", "https://signal.group/#SECRET!", false},
		{"bad protobuf", "https://signal.group/#" + base64.RawURLEncoding.EncodeToString([]byte{10, 255}), false},
		{"duplicate v1", "https://signal.group/#" + base64.RawURLEncoding.EncodeToString(append(append([]byte(nil), raw...), raw...)), false},
		{"conflicting version", "https://signal.group/#" + base64.RawURLEncoding.EncodeToString(append(append([]byte(nil), raw...), 18, 0)), false},
		{"unsupported version", "https://signal.group/#EgA", false},
		{"short master key", "https://signal.group/#" + joinFragment(31, 16), false},
		{"long key", "https://signal.group/#" + joinFragment(33, 16), false},
		{"short password", "https://signal.group/#" + joinFragment(32, 15), false},
		{"long password", "https://signal.group/#" + joinFragment(32, 17), false},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := signal.CheckGroupInviteLink(testCase.input)
			if testCase.valid {
				if err != nil {
					t.Fatal(err)
				}

				return
			}

			if !errors.Is(err, signal.ErrInvalidGroupInviteLink) {
				t.Fatalf("identity: %v", err)
			}

			for _, secret := range []string{testCase.input, fragment, "SECRET", strings.Repeat("K", 32), strings.Repeat("P", 16)} {
				if strings.Contains(err.Error(), secret) {
					t.Fatal("error leaked secret")
				}
			}
		})
	}
}

func TestGroupJoinOutcomeErrorSecrecy(t *testing.T) {
	t.Parallel()

	cause := &url.Error{Op: "PATCH", URL: "https://signal.group/#super-secret-key-password", Err: io.ErrClosedPipe}
	for _, accepted := range []bool{false, true} {
		wrapped := error(cause)
		if !accepted {
			wrapped = errors.Join(signal.ErrGroupUpdateUncertain, cause)
		}

		result := signal.GroupJoinResult{ID: "public-group-id", Revision: 8, Accepted: accepted}

		err := signal.GroupJoinOperationError(wrapped, result)
		if !errors.Is(err, io.ErrClosedPipe) || strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "public-group-id") {
			t.Fatal(err)
		}
	}
}
