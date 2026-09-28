package cmd

import (
	"fmt"
	"runtime"
	"runtime/debug"

	"github.com/cwbudde/go-signal/internal/signal"
	"github.com/spf13/cobra"
)

// Version information, set at build time via -ldflags.
//
//nolint:gochecknoglobals // ldflags -X can only target package-level variables
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version information",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "go-signal %s\n", version())
			fmt.Fprintf(out, "Git commit: %s\n", GitCommit)
			fmt.Fprintf(out, "Build date: %s\n", BuildDate)
			fmt.Fprintf(out, "backend: %s\n", signal.Backend)
			fmt.Fprintf(out, "signalmeow: %s\n", signal.SignalmeowVersion())
			fmt.Fprintf(out, "libsignal: %s\n", signal.LibsignalVersion)

			if libsignalGo := signal.LibsignalGoVersion(); libsignalGo != "" {
				fmt.Fprintf(out, "libsignal-go: %s\n", libsignalGo)
			}

			fmt.Fprintf(out, "Go version: %s\n", runtime.Version())
			fmt.Fprintf(out, "OS/Arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)
		},
	}
}

// version returns Version, or for a binary built without the ldflags the module version that
// `go install github.com/cwbudde/go-signal@vX.Y.Z` records in the build info.
func version() string {
	if Version != "dev" {
		return Version
	}

	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return Version
	}

	return info.Main.Version
}
