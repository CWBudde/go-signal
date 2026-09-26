// Command gendocs writes go-signal's man pages and shell completions for the release archives:
//
//	go run ./scripts/gendocs <out-dir>
//
// It creates <out-dir>/man (section 1) and <out-dir>/completions (bash, zsh, fish). The man page
// date comes from SOURCE_DATE_EPOCH when it is set, so that the output is reproducible. It needs
// no cgo: the command tree is built, never run.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cwbudde/go-signal/cmd"
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

const dirPerm = 0o755

var errUsage = errors.New("usage: gendocs <out-dir>")

func main() {
	err := run(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "gendocs:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 1 {
		return errUsage
	}

	out := args[0]

	root := cmd.NewRootCmd()
	root.DisableAutoGenTag = true
	// Cobra adds these lazily on Execute; add them now so that they get man pages too.
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	escapeTree(root)

	// The default data dir is resolved from the environment of whoever runs gendocs.
	dataDir := root.PersistentFlags().Lookup("data-dir")
	if dataDir != nil {
		dataDir.DefValue = "$XDG_DATA_HOME/go-signal"
	}

	err := genMan(root, filepath.Join(out, "man"))
	if err != nil {
		return err
	}

	return genCompletions(root, filepath.Join(out, "completions"))
}

// escapeTree keeps placeholders such as <recipient> in the man pages: cobra/doc renders Use,
// Short and Long as Markdown, which would drop them as HTML tags. Examples and flag usages are
// left alone, since cobra/doc puts them out verbatim, where escapes would show.
func escapeTree(command *cobra.Command) {
	escaper := strings.NewReplacer("<", `\<`, ">", `\>`)

	var walk func(*cobra.Command)

	walk = func(c *cobra.Command) {
		c.Use = escaper.Replace(c.Use)
		c.Short = escaper.Replace(c.Short)
		c.Long = escaper.Replace(c.Long)

		for _, sub := range c.Commands() {
			walk(sub)
		}
	}

	walk(command)
}

func genMan(root *cobra.Command, dir string) error {
	err := os.MkdirAll(dir, dirPerm)
	if err != nil {
		return fmt.Errorf("create man dir: %w", err)
	}

	date, err := sourceDate()
	if err != nil {
		return err
	}

	header := &doc.GenManHeader{Title: "GO-SIGNAL", Section: "1", Source: "go-signal", Date: date}

	err = doc.GenManTree(root, header, dir)
	if err != nil {
		return fmt.Errorf("generate man pages: %w", err)
	}

	return nil
}

// sourceDate returns SOURCE_DATE_EPOCH as a time, or nil (today) when it is unset.
func sourceDate() (*time.Time, error) {
	epoch := os.Getenv("SOURCE_DATE_EPOCH")
	if epoch == "" {
		return nil, nil //nolint:nilnil // nil means "now" to cobra/doc
	}

	sec, err := strconv.ParseInt(epoch, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("SOURCE_DATE_EPOCH: %w", err)
	}

	date := time.Unix(sec, 0).UTC()

	return &date, nil
}

func genCompletions(root *cobra.Command, dir string) error {
	err := os.MkdirAll(dir, dirPerm)
	if err != nil {
		return fmt.Errorf("create completions dir: %w", err)
	}

	gens := []struct {
		name string
		gen  func(string) error
	}{
		{"go-signal.bash", func(f string) error { return root.GenBashCompletionFileV2(f, true) }},
		{"_go-signal", root.GenZshCompletionFile},
		{"go-signal.fish", func(f string) error { return root.GenFishCompletionFile(f, true) }},
	}

	for _, g := range gens {
		err = g.gen(filepath.Join(dir, g.name))
		if err != nil {
			return fmt.Errorf("generate %s: %w", g.name, err)
		}
	}

	return nil
}
