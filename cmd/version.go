package cmd

import (
	"fmt"
	"runtime"

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
			fmt.Fprintf(out, "go-signal %s\n", Version)
			fmt.Fprintf(out, "Git commit: %s\n", GitCommit)
			fmt.Fprintf(out, "Build date: %s\n", BuildDate)
			fmt.Fprintf(out, "Go version: %s\n", runtime.Version())
			fmt.Fprintf(out, "OS/Arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)
		},
	}
}
