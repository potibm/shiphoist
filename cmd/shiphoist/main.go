package main

import (
	"fmt"
	"os"

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
		// main owns the only os.Exit in the program: cobra returns the error so
		// every command stays testable.
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}
}

// newRootCmd wires the CLI. It returns the command instead of running it so a
// test can drive it through cobra's own flag parsing.
func newRootCmd(d deps) *cobra.Command {
	var (
		forceRefresh bool
		verbose      bool
	)

	rootCmd := &cobra.Command{
		Use:   "shiphoist [path-to-docker-compose.yml]",
		Short: rootShort,
		Args:  cobra.ExactArgs(1),
		// A failure is reported once, by main. Printing it here as well would
		// duplicate it, and usage text would bury the actual cause.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(_ *cobra.Command, args []string) error {
			return runUpdate(d, args[0], forceRefresh, verbose)
		},
	}

	// Cobra's own output (version, usage, help) goes to the injected sinks too,
	// so nothing a test cannot see is ever written straight to the process.
	rootCmd.SetOut(d.Out)
	rootCmd.SetErr(d.ErrOut)

	flags := rootCmd.PersistentFlags()
	flags.BoolVarP(&forceRefresh, "force", "f", false, "Force refresh by bypassing the local cache")
	flags.BoolVarP(&verbose, "verbose", "v", false, "Report one line per image instead of a single progress bar")

	rootCmd.Version = fmt.Sprintf("%s (Commit: %s, Date: %s)", Version, Commit, Date)

	checkCmd := &cobra.Command{
		Use:   checkUse,
		Short: "Check a single Docker image for available updates",
		Long: `Check a single Docker image reference for available updates.
Example: shiphoist check ghcr.io/potibm/kasseapparat:2.18.0`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runCheck(d, args[0], forceRefresh)
		},
	}

	rootCmd.AddCommand(checkCmd)

	return rootCmd
}
