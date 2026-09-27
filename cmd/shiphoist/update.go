package main

import (
	"context"
	"fmt"
	"io"

	"github.com/potibm/shiphoist/internal/core"
	"github.com/potibm/shiphoist/internal/discovery"
	"github.com/potibm/shiphoist/internal/engine"
	"github.com/potibm/shiphoist/internal/patcher"
)

// runUpdate runs the full pipeline over filePath and applies the selected
// updates. It returns an error rather than exiting, leaving the process outcome
// to the caller.
func runUpdate(d deps, filePath string, forceRefresh, verbose bool) error {
	activeFetcher, err := d.NewFetcher(forceRefresh)
	if err != nil {
		return fmt.Errorf("failed to initialize fetcher: %w", err)
	}

	pipeline := &engine.Pipeline{
		Discoverer: &discovery.ComposeDiscoverer{},
		Fetcher:    activeFetcher,
		Patcher:    &patcher.FilePatcher{},
		Prompter:   d.NewPrompter(),
		Out:        d.Out,
		Verbose:    verbose,
	}

	if forceRefresh {
		fmt.Fprintln(d.Out, "🔄 Force refresh activated - bypassing cache...")
	}

	fmt.Fprintf(d.Out, "🚢 Hoisting sails for %s...\n", filePath)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	report, err := pipeline.ProcessFile(ctx, filePath)
	if err != nil {
		return err
	}

	printApplyResult(d.Out, report)

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
