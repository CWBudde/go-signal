package cmd_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/go-signal/cmd"
	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/output"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/cwbudde/go-signal/internal/signal/signaltest"
)

// errServerDown is a Devices failure that is not an unlink.
var errServerDown = errors.New("server unreachable")

// Flags of `mcp serve` and `mcp doctor`.
const (
	doctorCmd          = "doctor"
	allowRecipientFlag = "--allow-recipient"
	readOnlyFlag       = "--read-only"
	downloadDirFlag    = "--download-dir"
)

// missingDownloadDir is a download dir that doesn't exist, so that golden output has no temp path.
const missingDownloadDir = "/nonexistent/go-signal/attachments"

func doctorArgs(extra ...string) []string {
	return append([]string{mcpCmd, doctorCmd, allowRecipientFlag, aliceNumber, downloadDirFlag, missingDownloadDir},
		extra...)
}

func TestMCPDoctor(t *testing.T) {
	t.Parallel()

	for _, format := range []output.Format{output.Plain, output.JSON} {
		t.Run(string(format), func(t *testing.T) {
			t.Parallel()

			fake := &signaltest.Fake{Linked: []signal.Account{*testAccount()}, Devices: testDevices()}

			out, err := run(t, fake, append([]string{"-o", string(format)}, doctorArgs()...)...)
			if err != nil {
				t.Fatalf("mcp doctor: %v", err)
			}

			name := "mcp_doctor"
			if format == output.JSON {
				name += "_json"
			}

			golden(t, name, out)

			if len(fake.Connects()) != 0 {
				t.Error("mcp doctor connected")
			}
		})
	}
}

func TestMCPDoctorUnlinked(t *testing.T) {
	t.Parallel()

	acc := *testAccount()
	acc.UnlinkedAt = time.Date(2026, 9, 21, 8, 0, 0, 0, time.UTC)

	out, err := run(t, &signaltest.Fake{Linked: []signal.Account{acc}}, doctorArgs()...)
	if !errors.Is(err, app.ErrUnhealthy) || cmd.ExitCode(err) != cmd.ExitUnlinked {
		t.Fatalf("got %v (exit %d), want ErrUnhealthy with exit code %d", err, cmd.ExitCode(err), cmd.ExitUnlinked)
	}

	golden(t, "mcp_doctor_unlinked", out)
}

// doctorFinding is a case of TestMCPDoctorFindings.
type doctorFinding struct {
	name string
	fake *signaltest.Fake
	args []string
	// want are lines (or their beginnings) of the output.
	want    []string
	wantErr bool
}

func TestMCPDoctorFindings(t *testing.T) {
	t.Parallel()

	for _, test := range doctorFindings(t) {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			out, err := run(t, test.fake, test.args...)
			if test.wantErr != (err != nil) {
				t.Fatalf("error = %v, want error %v\n%s", err, test.wantErr, out)
			}

			if test.wantErr && cmd.ExitCode(err) != cmd.ExitFailure {
				t.Errorf("exit code = %d, want %d", cmd.ExitCode(err), cmd.ExitFailure)
			}

			for _, want := range test.want {
				if !hasLinePrefix(out, want) {
					t.Errorf("output has no line starting with %q:\n%s", want, out)
				}
			}
		})
	}
}

//nolint:funlen // table-driven
func doctorFindings(t *testing.T) []doctorFinding {
	t.Helper()

	notADir := filepath.Join(t.TempDir(), "file")

	err := os.WriteFile(notADir, nil, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return []doctorFinding{
		{
			name: "lock held",
			fake: &signaltest.Fake{Linked: []signal.Account{*testAccount()}, Devices: testDevices(), InUse: true},
			args: doctorArgs(),
			want: []string{"warn  lock"},
		},
		{
			name: "offline",
			fake: &signaltest.Fake{Linked: []signal.Account{*testAccount()}, DevicesErr: errServerDown},
			args: doctorArgs("--offline"),
			want: []string{"ok    account", "ok    inbox"},
		},
		{
			name:    "server unreachable",
			fake:    &signaltest.Fake{Linked: []signal.Account{*testAccount()}, DevicesErr: errServerDown},
			args:    doctorArgs(),
			want:    []string{"fail  server"},
			wantErr: true,
		},
		{
			name:    "not linked",
			fake:    &signaltest.Fake{},
			args:    doctorArgs(),
			want:    []string{"fail  account", "hint: link this device"},
			wantErr: true,
		},
		{
			name:    "open fails",
			fake:    &signaltest.Fake{OpenErr: signal.ErrCGORequired},
			args:    doctorArgs(),
			want:    []string{"fail  client", "hint: use a release binary"},
			wantErr: true,
		},
		{
			name: "attach dir missing",
			fake: &signaltest.Fake{Linked: []signal.Account{*testAccount()}, Devices: testDevices()},
			args: doctorArgs("--attach-dir", filepath.Join(t.TempDir(), "missing")),
			// The account is checked all the same.
			want:    []string{"fail  policy", "ok    account"},
			wantErr: true,
		},
		{
			name:    "listen without token",
			fake:    &signaltest.Fake{Linked: []signal.Account{*testAccount()}, Devices: testDevices()},
			args:    doctorArgs("--listen", "127.0.0.1:0"),
			want:    []string{"fail  listen"},
			wantErr: true,
		},
		{
			name: "everyone allowed",
			fake: &signaltest.Fake{Linked: []signal.Account{*testAccount()}, Devices: testDevices()},
			args: []string{mcpCmd, doctorCmd, allowRecipientFlag, "*", downloadDirFlag, missingDownloadDir},
			want: []string{"warn  policy        sends to anyone"},
		},
		{
			name: "read-only",
			fake: &signaltest.Fake{Linked: []signal.Account{*testAccount()}, Devices: testDevices()},
			args: []string{mcpCmd, doctorCmd, readOnlyFlag, downloadDirFlag, missingDownloadDir},
			want: []string{"ok    policy        read-only"},
		},
		{
			name:    "download dir is a file",
			fake:    &signaltest.Fake{Linked: []signal.Account{*testAccount()}, Devices: testDevices()},
			args:    []string{mcpCmd, doctorCmd, readOnlyFlag, downloadDirFlag, notADir},
			want:    []string{"fail  download dir  " + notADir + ": not a directory"},
			wantErr: true,
		},
		{
			name: "default download dir",
			fake: &signaltest.Fake{Linked: []signal.Account{*testAccount()}, Devices: testDevices()},
			args: []string{mcpCmd, doctorCmd, readOnlyFlag},
			want: []string{"ok download dir /"},
		},
	}
}

// hasLinePrefix reports whether a line of out starts with prefix, with runs of white space
// counting as one space (the columns' widths vary).
func hasLinePrefix(out, prefix string) bool {
	prefix = strings.Join(strings.Fields(prefix), " ")

	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(strings.Join(strings.Fields(line), " "), prefix) {
			return true
		}
	}

	return false
}
