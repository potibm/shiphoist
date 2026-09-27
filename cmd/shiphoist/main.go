package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

const (
	rootShort = "Interactively update and SHA256-pin Docker images in Compose files"
	checkUse  = "check [image-reference]"
)

func main() {
	if err := newRootCmd(newDeps()).Execute(); err != nil {
		// errIncompleteRun is a run outcome, not a malfunction: the report
		// already names the images, so printing it here would only duplicate
		// that. It still has to reach the exit code.
		if !errors.Is(err, errIncompleteRun) {
			fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		}

		os.Exit(1)
	}
}

// newRootCmd wires the CLI. It returns the command instead of running it so a
// test can drive it through cobra's own flag parsing.
func newRootCmd(d deps) *cobra.Command {
	var opts options

	rootCmd := &cobra.Command{
		Use:   "shiphoist [path-to-docker-compose.yml]",
		Short: rootShort,
		Args:  cobra.ExactArgs(1),
		// A failure is reported once, by main. Printing it here as well would
		// duplicate it, and usage text would bury the actual cause.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, args []string) error {
			return runUpdate(d, opts, args[0])
		},
	}

	// Cobra's own output (version, usage, help) goes to the injected sinks too,
	// so nothing a test cannot see is ever written straight to the process.
	rootCmd.SetOut(d.Out)
	rootCmd.SetErr(d.ErrOut)

	registerSharedFlags(rootCmd, &opts)
	registerUpdateFlags(rootCmd, &opts)

	rootCmd.Version = fmt.Sprintf("%s (Commit: %s, Date: %s)", Version, Commit, Date)

	checkCmd := &cobra.Command{
		Use:   checkUse,
		Short: "Check a single Docker image for available updates",
		Long: `Check a single Docker image reference for available updates.
Example: shiphoist check ghcr.io/potibm/kasseapparat:2.18.0`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runCheck(d, opts, args[0])
		},
	}

	rootCmd.AddCommand(checkCmd)

	return rootCmd
}

// registerSharedFlags binds the flags both commands understand. They are
// persistent so `check` can reuse them.
func registerSharedFlags(rootCmd *cobra.Command, opts *options) {
	flags := rootCmd.PersistentFlags()

	flags.BoolVarP(&opts.ForceRefresh, "force", "f", false, "Force refresh by bypassing the local cache")
	flags.BoolVarP(&opts.Verbose, "verbose", "v", false, "Report one line per image instead of a single progress bar")
}

// registerUpdateFlags binds the flags that only mean something when a file is
// updated.
//
// They are local rather than persistent so that `check --json` is a usage error
// instead of being silently ignored: a flag that quietly does nothing is worse
// than one that refuses.
func registerUpdateFlags(rootCmd *cobra.Command, opts *options) {
	flags := rootCmd.Flags()

	flags.BoolVar(&opts.DryRun, "dry-run", false, "Report the updates that would apply, without writing them")
	flags.BoolVarP(&opts.Yes, "yes", "y", false, "Apply every update within --mode without asking")
	flags.BoolVar(&opts.JSON, "json", false, "Write a machine-readable report to stdout, keeping progress on stderr")
	flags.BoolVarP(&opts.Quiet, "quiet", "q", false, "Suppress the progress bar, the summary and skipped images")
	flags.StringVar(&opts.Mode, "mode", "", "Limit updates to "+strings.Join(modeNames, ", "))
	flags.StringVar(&opts.Exclude, "exclude", "", "Skip images whose repository matches this regular expression")
}
