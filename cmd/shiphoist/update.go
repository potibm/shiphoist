package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/potibm/shiphoist/internal/core"
	"github.com/potibm/shiphoist/internal/engine"
	"github.com/potibm/shiphoist/internal/patcher"
)

// errNoTerminal is returned when the run needs a human and has nowhere to ask.
var errNoTerminal = errors.New("no terminal to prompt on")

// runUpdate runs the full pipeline over filePath and applies the selected
// updates. It returns an error rather than exiting, leaving the process outcome
// to the caller.
func runUpdate(d deps, opts options, filePath string) error {
	if err := assertFlagCombination(opts); err != nil {
		return err
	}

	maxUpdate, err := opts.maxUpdate()
	if err != nil {
		return err
	}

	discoverer, err := buildDiscoverer(opts)
	if err != nil {
		return err
	}

	if !opts.Yes && !d.IsInteractive() {
		return fmt.Errorf(
			"%w: use --yes to apply every update within --mode, optionally with --dry-run to preview only",
			errNoTerminal,
		)
	}

	activeFetcher, err := d.NewFetcher(opts.ForceRefresh)
	if err != nil {
		return fmt.Errorf("failed to initialize fetcher: %w", err)
	}

	human := humanOut(d, opts)

	pipeline := &engine.Pipeline{
		Discoverer: discoverer,
		Fetcher:    activeFetcher,
		Patcher:    &patcher.FilePatcher{},
		Prompter:   buildPrompter(d, opts, maxUpdate),
		Out:        human,
		Verbose:    opts.Verbose,
		DryRun:     opts.DryRun,
		Quiet:      opts.Quiet,
	}

	if !opts.Quiet {
		printBanner(human, opts, filePath)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	report, err := pipeline.ProcessFile(ctx, filePath)
	if err != nil {
		return err
	}

	report.Filtered = filteredFrom(discoverer)

	if opts.JSON {
		if err := writeReportJSON(d.Out, report); err != nil {
			return err
		}
	}

	printApplyResult(human, report)

	if !opts.Quiet {
		printFiltered(human, report)
	}

	return exitCodeFor(opts, report)
}

// printFiltered reports the references that were never checked, so a run cannot
// quietly cover less of the file than it appears to.
func printFiltered(w io.Writer, report *core.Report) {
	if len(report.Filtered) == 0 {
		return
	}

	fmt.Fprintf(w, "\n🚫 Not checked (%d):\n", len(report.Filtered))

	for _, f := range report.Filtered {
		fmt.Fprintf(w, "   • %s (line %d): %s\n", f.Image, f.LineNumber, f.Reason)
	}
}

// printBanner introduces the run. It is pure chrome, so --quiet drops it along
// with the progress reporter's own output.
func printBanner(w io.Writer, opts options, filePath string) {
	if opts.ForceRefresh {
		fmt.Fprintln(w, "🔄 Force refresh activated - bypassing cache...")
	}

	fmt.Fprintf(w, "🚢 Hoisting sails for %s...\n", filePath)
}

// writeReportJSON writes the report as the single document on w.
//
// Indented rather than compact because this is as often read in a CI log by a
// human as piped into jq.
func writeReportJSON(w io.Writer, report *core.Report) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(report); err != nil {
		return fmt.Errorf("failed to write report: %w", err)
	}

	return nil
}

// printApplyResult reports what the run did. An empty result means every
// discovered reference was already current.
func printApplyResult(w io.Writer, report *core.Report) {
	if len(report.Updates) == 0 {
		fmt.Fprintln(w, "✅ Everything is up to date! No changes needed.")

		return
	}

	fmt.Fprintf(w, "%s\n\n", applyHeadline(report))

	for _, u := range report.Updates {
		if u.UpdateType == core.UpdateTypeNone {
			fmt.Fprintf(w, "  📌 %s: pinned to new digest\n", u.Label())

			continue
		}

		fmt.Fprintf(w, "  🚀 %s: %s -> %s (%s)\n", u.Label(), u.OldTag, u.NewTag, u.UpdateType)
	}
}

// applyHeadline states the outcome. A dry run must not read like a write, so it
// is called out explicitly rather than by omission.
func applyHeadline(report *core.Report) string {
	count := fmt.Sprintf("%d %s", len(report.Updates), plural(len(report.Updates), "update", "updates"))

	if report.DryRun {
		return fmt.Sprintf("🔍 Would apply %s (dry run, nothing was written):", count)
	}

	return fmt.Sprintf("✅ Successfully applied %s:", count)
}

func plural(n int, singular, many string) string {
	if n == 1 {
		return singular
	}

	return many
}
