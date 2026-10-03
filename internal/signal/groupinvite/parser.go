// Package groupinvite validates sensitive invite links without a protocol backend.
package groupinvite

import (
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"unicode"

	"google.golang.org/protobuf/encoding/protowire"
)

// ErrInvalid never includes supplied or decoded data.
var ErrInvalid = errors.New("invalid group invite link")

const (
	maxInputSize   = 4096
	masterKeySize  = 32
	passwordSize   = 16
	masterKeyField = 1
	passwordField  = 2
)

// Invite owns its decoded secret bytes. It must never be rendered or logged.
type Invite struct {
	MasterKey []byte
	Password  []byte
}

// Parse accepts only bounded v1 links with exactly sized secrets.
func Parse(input string) (Invite, error) {
	if len(input) > maxInputSize {
		return Invite{}, ErrInvalid
	}

	fragment, err := inviteFragment(strings.TrimSpace(input))
	if err != nil {
		return Invite{}, ErrInvalid
	}

	raw, err := base64.RawURLEncoding.Strict().DecodeString(fragment)
	if err != nil {
		raw, err = base64.URLEncoding.Strict().DecodeString(fragment)
	}

	if err != nil {
		return Invite{}, ErrInvalid
	}

	contents, rest, ok := field(raw, masterKeyField)
	if !ok || len(rest) != 0 {
		return Invite{}, ErrInvalid
	}

	return parseContents(contents)
}

//nolint:cyclop // Each URL component has an independent secrecy boundary.
func inviteFragment(input string) (string, error) {
	if strings.ContainsFunc(input, unicode.IsSpace) {
		return "", ErrInvalid
	}

	parsed, err := url.Parse(input)
	if err != nil {
		return "", ErrInvalid
	}

	if !strings.EqualFold(parsed.Scheme, "https") && !strings.EqualFold(parsed.Scheme, "sgnl") {
		return "", ErrInvalid
	}

	if !strings.EqualFold(parsed.Host, "signal.group") || parsed.User != nil {
		return "", ErrInvalid
	}

	if parsed.RawQuery != "" || parsed.ForceQuery || (parsed.Path != "" && parsed.Path != "/") {
		return "", ErrInvalid
	}

	if parsed.RawPath != "" || parsed.Fragment == "" || parsed.RawFragment != "" {
		return "", ErrInvalid
	}

	return parsed.Fragment, nil
}

func parseContents(contents []byte) (Invite, error) {
	var fields [passwordField][]byte

	for len(contents) > 0 {
		number, kind, n := protowire.ConsumeTag(contents)
		if n < 0 || kind != protowire.BytesType || number < masterKeyField || number > passwordField {
			return Invite{}, ErrInvalid
		}

		value, consumed := protowire.ConsumeBytes(contents[n:])
		if consumed < 0 || fields[number-1] != nil {
			return Invite{}, ErrInvalid
		}

		fields[number-1] = append([]byte{}, value...)
		contents = contents[n+consumed:]
	}

	if len(fields[0]) != masterKeySize || len(fields[1]) != passwordSize {
		return Invite{}, ErrInvalid
	}

	return Invite{MasterKey: fields[0], Password: fields[1]}, nil
}

func field(raw []byte, want protowire.Number) ([]byte, []byte, bool) {
	number, kind, n := protowire.ConsumeTag(raw)
	if n < 0 || number != want || kind != protowire.BytesType {
		return nil, nil, false
	}

	value, consumed := protowire.ConsumeBytes(raw[n:])
	if consumed < 0 {
		return nil, nil, false
	}

	return value, raw[n+consumed:], true
}
