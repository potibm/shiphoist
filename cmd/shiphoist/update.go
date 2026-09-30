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

	discoverer, err := buildDiscoverer(opts, filePath)
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

	printApplyResult(human, opts, report)

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
//
// An empty result is not the same as a clean one: images whose registry lookup
// failed are absent from Updates, so a run where every fetch failed would
// otherwise claim to be up to date. Under --quiet the progress reporter prints
// no failure block, so the list is repeated here — the exit code alone is not
// enough to explain an empty result.
func printApplyResult(w io.Writer, opts options, report *core.Report) {
	if len(report.Updates) == 0 {
		printNoUpdates(w, opts, report)

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

	printUnresolved(w, opts, report)
}

// printNoUpdates states an empty result, distinguishing a run that checked
// everything and found nothing from one that could not check everything.
func printNoUpdates(w io.Writer, opts options, report *core.Report) {
	if len(report.Failures) == 0 {
		fmt.Fprintln(w, "✅ Everything is up to date! No changes needed.")

		return
	}

	failures := counted(len(report.Failures), "image", "images")

	fmt.Fprintf(w, "⚠️  No updates applied - %s could not be checked.\n", failures)

	printFailureBullets(w, opts, report.Failures)
}

// printUnresolved names the images that were never resolved, so a run that
// applied some updates cannot imply it covered the whole file.
func printUnresolved(w io.Writer, opts options, report *core.Report) {
	if len(report.Failures) == 0 {
		return
	}

	fmt.Fprintf(w, "\n⚠️  %s could not be checked.\n", counted(len(report.Failures), "image", "images"))

	printFailureBullets(w, opts, report.Failures)
}

// printFailureBullets lists each unresolved image with its reason.
//
// A quiet run gets the list because ui.Progress.Stop suppresses its own failure
// block there. A non-quiet run already saw it, so repeating it would be noise.
func printFailureBullets(w io.Writer, opts options, failures []core.Failure) {
	if !opts.Quiet {
		return
	}

	for _, f := range failures {
		fmt.Fprintf(w, "   • %s: %s\n", f.Image, f.Message)
	}
}

// applyHeadline states the outcome. A dry run must not read like a write, so it
// is called out explicitly rather than by omission.
func applyHeadline(report *core.Report) string {
	count := counted(len(report.Updates), "update", "updates")

	if report.DryRun {
		return fmt.Sprintf("🔍 Would apply %s (dry run, nothing was written):", count)
	}

	return fmt.Sprintf("✅ Successfully applied %s:", count)
}

// counted renders a count with the noun agreeing with it.
func counted(n int, singular, many string) string {
	return fmt.Sprintf("%d %s", n, plural(n, singular, many))
}

func plural(n int, singular, many string) string {
	if n == 1 {
		return singular
	}

	return many
}
