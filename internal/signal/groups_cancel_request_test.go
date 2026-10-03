//nolint:cyclop,goconst,lll // Independent result policy and reference validation cases.
package signal_test

import (
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/cwbudde/go-signal/internal/signal"
)

func TestGroupCancelRequestOperationError(t *testing.T) {
	t.Parallel()

	secret := &url.Error{Op: http.MethodPatch, URL: "https://private-secret/key", Err: io.ErrClosedPipe}
	groupID := base64.StdEncoding.EncodeToString(make([]byte, 32))

	tests := []struct {
		name     string
		result   signal.GroupCancelRequestResult
		cause    error
		contains string
	}{
		{"accepted unverified", signal.GroupCancelRequestResult{ID: groupID, Revision: 8, Accepted: true, Changed: true}, secret, "accepted"},
		{"accepted verified canceled", signal.GroupCancelRequestResult{ID: groupID, Revision: 8, Accepted: true, Changed: true, Verified: true}, secret, "accepted"},
		{"uncertain cancellation", signal.GroupCancelRequestResult{ID: groupID, Revision: 8}, errors.Join(signal.ErrGroupUpdateUncertain, secret), "cancellation is uncertain"},
		{"cancellation conflict", signal.GroupCancelRequestResult{}, errors.Join(signal.ErrGroupChanged, secret), "group changed"},
		{"cancellation terminated", signal.GroupCancelRequestResult{}, errors.Join(signal.ErrGroupTerminated, secret), "group is terminated"},
		{"unknown cancellation group", signal.GroupCancelRequestResult{ID: "private-secret"}, errors.Join(signal.ErrUnknownGroup, secret), "not known to this account"},
		{"arbitrary", signal.GroupCancelRequestResult{}, secret, "cancellation failed"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := signal.GroupCancelRequestOperationError(testCase.cause, testCase.result)

			var nested *url.Error
			if !errors.Is(err, io.ErrClosedPipe) || !errors.As(err, &nested) || nested != secret || strings.Contains(err.Error(), "private-secret") || !strings.Contains(err.Error(), testCase.contains) {
				t.Fatal(err)
			}

			if testCase.result.ID == groupID && testCase.result.Accepted && (!strings.Contains(err.Error(), groupID) || !strings.Contains(err.Error(), "8")) {
				t.Fatal("lost accepted evidence", err)
			}
		})
	}
}

func TestGroupCancelRequestReference(t *testing.T) {
	t.Parallel()

	for _, ref := range []string{"", "  ", strings.Repeat(" ", 4097), "group:bad", "sgnl://signal.group/#private-secret", "HTTPS://user@SIGNAL.GROUP/%zz#private-secret"} {
		err := signal.CheckGroupCancelRequestReference(ref)
		if !errors.Is(err, signal.ErrUnknownGroup) || strings.Contains(err.Error(), "private-secret") {
			t.Fatal(ref, err)
		}
	}

	raw := []byte(strings.Repeat("K", 32))

	canonical := base64.StdEncoding.EncodeToString(raw)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		ref := " group:" + enc.EncodeToString(raw) + " "

		err := signal.CheckGroupCancelRequestReference(ref)
		if err != nil {
			t.Fatal(err)
		}

		if got := signal.NormalizeGroupCancelRequestReference(ref); got != canonical {
			t.Fatal(got)
		}
	}
}
