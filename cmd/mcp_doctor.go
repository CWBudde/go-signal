package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/cwbudde/go-signal/internal/app"
	"github.com/cwbudde/go-signal/internal/mcp"
	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/spf13/cobra"
)

const mcpDoctorLong = `Check whether "mcp serve" with the same flags and config would work, and why not:

  go-signal mcp doctor --allow-recipient +4915112345678

It checks the safety settings and --listen, that the account is linked, that no other process
holds it (a warning: that is expected while your MCP client runs the server), that Signal's server
still lists this device (unless --offline), that the inbox can be read, and that attachments can
be saved to the download dir. Each check prints ok, warn or fail, with a hint what to do.

The exit code is 0 when no check failed (warnings are fine), 3 when this device was unlinked, and
1 for other failures. A running server answers the same questions through its doctor tool.`

func newMCPDoctorCmd(clients *clientOpener, printers *printerFactory) *cobra.Command {
	var (
		dlDir   string
		offline bool
	)

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check the setup of the MCP server",
		Long:  mcpDoctorLong,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			printer, err := printers.printer(cmd.OutOrStdout())
			if err != nil {
				return err
			}

			bindMCPFlags(clients.cfg, cmd)

			checks := doctorConfig(clients)
			checks = append(checks, doctorAccount(cmd.Context(), clients, dlDir, offline)...)

			err = printer.Doctor(checks)
			if err != nil {
				return err //nolint:wrapcheck // output wraps it
			}

			return app.DoctorError(checks)
		},
	}

	addMCPFlags(cmd, &dlDir)
	cmd.Flags().BoolVar(&offline, "offline", false, "don't ask Signal's server whether this device is still linked")

	return cmd
}

// doctorConfig checks the safety settings and --listen as `mcp serve` reads them.
func doctorConfig(clients *clientOpener) []app.Check {
	var checks []app.Check

	pol, err := loadPolicy(clients.cfg)
	if err != nil {
		checks = append(checks, app.Check{Name: "policy", Status: app.CheckFail, Detail: err.Error(), Err: err})
	} else {
		checks = append(checks, app.PolicyCheck(pol.readOnly, pol.allow, pol.attachDir, pol.confirm))
	}

	checks = append(checks, hookCheck(clients, pol))

	listen, err := loadListen(clients.cfg)

	switch {
	case err != nil:
		checks = append(checks, app.Check{Name: "listen", Status: app.CheckFail, Detail: err.Error(), Err: err})
	case listen.addr == "":
		checks = append(checks, app.Check{Name: "transport", Status: app.CheckOK, Detail: "stdin/stdout"})
	default:
		checks = append(checks, app.Check{
			Name: "transport", Status: app.CheckOK,
			Detail: "streamable HTTP at http://" + listen.addr + mcp.HTTPPath + " with a bearer token",
		})
	}

	return checks
}

// hookCheckName names the check of --on-message.
const hookCheckName = "hook"

// hookCheck checks --on-message and its settings.
func hookCheck(clients *clientOpener, pol policy) app.Check {
	hook, err := loadHook(clients.cfg, pol)

	switch {
	case err != nil:
		return app.Check{Name: hookCheckName, Status: app.CheckFail, Detail: err.Error(), Err: err}
	case hook.program == "":
		return app.Check{Name: hookCheckName, Status: app.CheckOK, Detail: "off"}
	}

	from := fmt.Sprintf("%d --hook-from entries", hook.from.Len())
	if hook.from.All() {
		from = "everyone"
	}

	timeout := "no timeout"
	if hook.timeout > 0 {
		timeout = "timeout " + hook.timeout.String()
	}

	return app.Check{
		Name: hookCheckName, Status: app.CheckOK,
		Detail: fmt.Sprintf("%s for messages of %s, %s", hook.program, from, timeout),
	}
}

// doctorAccount checks the account (see app.Doctor) and the download dir.
func doctorAccount(ctx context.Context, clients *clientOpener, dlDir string, offline bool) []app.Check {
	client, err := clients.open(ctx)
	if err != nil {
		check := app.Check{Name: "client", Status: app.CheckFail, Detail: err.Error(), Err: err}
		if errors.Is(err, signal.ErrCGORequired) {
			check.Hint = "use a release binary, or build with cgo and libsignal (`just build`)"
		}

		return []app.Check{check}
	}
	defer closeClient(client)

	checks := app.New(client).Doctor(ctx, app.DoctorRequest{Lock: true, Server: !offline})
	if checks[0].Status == app.CheckFail {
		return checks
	}

	if dlDir == "" {
		dlDir, err = defaultDownloadDir(ctx, clients, client)
		if err != nil {
			return append(checks, app.Check{Name: "download dir", Status: app.CheckFail, Detail: err.Error(), Err: err})
		}
	}

	return append(checks, app.DownloadDirCheck(dlDir))
}
