//nolint:lll // Independent invitation-policy and secrecy matrices.
package signal_test

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

const (
	acceptSecretMarker = "acceptance-private-material"
	acceptFailureText  = "acceptance failed"
)

func TestCheckAcceptInvitation(t *testing.T) {
	t.Parallel()

	self := signal.Recipient{ACI: selfACI, PNI: bobACI}

	aci := signal.Recipient{ACI: self.ACI}

	pni := signal.Recipient{PNI: self.PNI}

	pending := func(r signal.Recipient) signal.PendingMember {
		return signal.PendingMember{Recipient: r, Role: signal.GroupRoleAdmin}
	}

	tests := []struct {
		name                  string
		group                 signal.Group
		self                  signal.Recipient
		want                  signal.Recipient
		noop, invalid, absent bool
	}{
		{name: "ACI", group: signal.Group{Pending: []signal.PendingMember{pending(aci)}}, want: aci},
		{name: "PNI", group: signal.Group{Pending: []signal.PendingMember{pending(pni)}}, want: pni},
		{name: "ACI priority", group: signal.Group{Pending: []signal.PendingMember{pending(pni), pending(aci)}}, want: aci},
		{name: "typed ACI collision", group: signal.Group{Pending: []signal.PendingMember{pending(signal.Recipient{ACI: self.PNI})}}, absent: true},
		{name: "typed PNI collision", group: signal.Group{Pending: []signal.PendingMember{pending(signal.Recipient{PNI: self.ACI})}}, absent: true},
		{name: "zero own PNI", self: signal.Recipient{ACI: self.ACI, PNI: nilACITestValue}, group: signal.Group{Pending: []signal.PendingMember{pending(signal.Recipient{PNI: nilACITestValue})}}, absent: true},
		{name: "no own PNI", self: aci, group: signal.Group{Pending: []signal.PendingMember{pending(pni)}}, absent: true},
		{name: "foreign", group: signal.Group{Pending: []signal.PendingMember{pending(signal.Recipient{ACI: "33333333-3333-4333-8333-333333333333"})}}, absent: true},
		{name: "no invitation", absent: true},
		{name: "duplicate ACI", group: signal.Group{Pending: []signal.PendingMember{pending(aci), pending(aci)}}, invalid: true},
		{name: "duplicate PNI", group: signal.Group{Pending: []signal.PendingMember{pending(pni), pending(pni)}}, invalid: true},
		{name: "unknown role", group: signal.Group{Pending: []signal.PendingMember{{Recipient: aci}}}, invalid: true},
		{name: "invalid role", group: signal.Group{Pending: []signal.PendingMember{{Recipient: aci, Role: signal.GroupRole(42)}}}, invalid: true},
		{name: "own requester", group: signal.Group{Requesting: []signal.RequestingMember{{Recipient: aci}}}, absent: true},
		{name: "request plus invitation", group: signal.Group{Pending: []signal.PendingMember{pending(pni)}, Requesting: []signal.RequestingMember{{Recipient: aci}}}, invalid: true},
		{name: "banned ACI", group: signal.Group{Pending: []signal.PendingMember{pending(aci)}, Banned: []signal.BannedMember{{Recipient: aci}}}, invalid: true},
		{name: "banned PNI", group: signal.Group{Pending: []signal.PendingMember{pending(aci)}, Banned: []signal.BannedMember{{Recipient: pni}}}, invalid: true},
		{name: "foreign ban", group: signal.Group{Pending: []signal.PendingMember{pending(aci)}, Banned: []signal.BannedMember{{Recipient: signal.Recipient{ACI: self.PNI}}}}, want: aci},
		{name: "overflow", group: signal.Group{Revision: math.MaxUint32, Pending: []signal.PendingMember{pending(aci)}}, invalid: true},
		{name: "member recipient has both IDs", group: signal.Group{Members: []signal.GroupMember{{Recipient: self, Role: signal.GroupRoleMember}}}, noop: true},
		{name: "member wins stale state", group: signal.Group{Revision: math.MaxUint32, Members: []signal.GroupMember{{Recipient: aci, Role: signal.GroupRoleMember}}, Pending: []signal.PendingMember{{Recipient: pni}, {Recipient: pni}}, Requesting: []signal.RequestingMember{{Recipient: aci}}, Banned: []signal.BannedMember{{Recipient: pni}}}, noop: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			own := test.self

			if own.IsZero() {
				own = self
			}

			got, noop, err := test.group.CheckAcceptInvitation(own)

			if got != test.want || noop != test.noop || (err != nil) != (test.invalid || test.absent) || errors.Is(err, signal.ErrGroupInvitationNotFound) != test.absent {
				t.Fatalf("selection %+v,%t,%v", got, noop, err)
			}
		})
	}
}

func TestGroupAcceptResultErrors(t *testing.T) {
	t.Parallel()

	gid := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("G", 32)))

	secret := &url.Error{Op: http.MethodPatch, URL: "https://acceptance-private-material/password", Err: io.ErrClosedPipe}

	tests := []struct {
		name     string
		cause    error
		result   signal.GroupAcceptResult
		contains string
	}{
		{"HTTP acceptance", secret, signal.GroupAcceptResult{ID: gid, Revision: 8, Accepted: true}, "invitation accepted"},
		{"ambiguous submission", errors.Join(signal.ErrGroupUpdateUncertain, secret), signal.GroupAcceptResult{ID: gid, Revision: 8}, "attempted revision 8"},
		{"submission conflict", errors.Join(signal.ErrGroupChanged, secret), signal.GroupAcceptResult{}, "group changed"},
		{"missing invitation", errors.Join(signal.ErrGroupInvitationNotFound, secret), signal.GroupAcceptResult{}, "invitation"},
		{"terminated server", errors.Join(signal.ErrGroupTerminated, secret), signal.GroupAcceptResult{}, "group is terminated"},
		{"canceled", errors.Join(context.Canceled, secret), signal.GroupAcceptResult{}, acceptFailureText},
		{"unlinked", errors.Join(signal.ErrDeviceUnlinked, secret), signal.GroupAcceptResult{}, acceptFailureText},
		{"arbitrary", secret, signal.GroupAcceptResult{}, acceptFailureText},
		{"unclassified ID", secret, signal.GroupAcceptResult{ID: acceptSecretMarker, Revision: 8, Accepted: true}, "invitation accepted"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := signal.GroupAcceptOperationError(test.cause, test.result)

			if !errors.Is(err, test.cause) || !errors.Is(err, io.ErrClosedPipe) || !strings.Contains(err.Error(), test.contains) || strings.Contains(err.Error(), acceptSecretMarker) || strings.Contains(err.Error(), "password") {
				t.Fatal(err)
			}

			if test.result.ID == gid && (!strings.Contains(err.Error(), gid) || !strings.Contains(err.Error(), "groups show")) {
				t.Fatal(err)
			}
		})
	}
}

func TestGroupAcceptReferencePreflight(t *testing.T) {
	t.Parallel()

	for _, ref := range []string{
		"", "  ", strings.Repeat(" ", 4097) + "title", "group:secret", "https://signal.group/#secret", "sgnl://signal.group/#secret",
		"https://signal.group/#%zz", "sgnl://signal.group/#%zz",
		"  HTTPS://SIGNAL.GROUP:443/#%zz  ", "https://%zz@signal.group/#%zz",
		"https://signal.group:bad-port/#%zz", "sgnl:%zz",
	} {
		err := signal.CheckGroupAcceptReference(ref)

		if err == nil || strings.Contains(err.Error(), acceptSecretMarker) {
			t.Fatalf("preflight %v", err)
		}
	}

	for _, ref := range []string{"title", "group:" + base64.StdEncoding.EncodeToString(make([]byte, 32)), "known key", "https://ordinary.example/#%zz", "sgnl project title"} {
		err := signal.CheckGroupAcceptReference(ref)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestNormalizeGroupAcceptReference(t *testing.T) {
	t.Parallel()

	raw := []byte(strings.Repeat("\xff", 32))

	canonical := base64.StdEncoding.EncodeToString(raw)
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		ref := encoding.EncodeToString(raw)
		for _, alias := range []string{ref, "  group:" + ref + "  "} {
			if got := signal.NormalizeGroupAcceptReference(alias); got != canonical {
				t.Fatalf("normalized reference %q", got)
			}
		}
	}

	if got := signal.NormalizeGroupAcceptReference("  legacy alias  "); got != "legacy alias" {
		t.Fatal(got)
	}
}
