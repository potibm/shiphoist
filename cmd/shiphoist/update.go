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

	updates, err := pipeline.ProcessFile(ctx, filePath)
	if err != nil {
		return err
	}

	printApplyResult(d.Out, updates)

	return nil
}

// printApplyResult reports what the run did. An empty result means every
// discovered reference was already current.
func printApplyResult(w io.Writer, updates []core.ImageUpdate) {
	if len(updates) == 0 {
		fmt.Fprintln(w, "✅ Everything is up to date! No changes needed.")

		return
	}

	fmt.Fprintf(w, "✅ Successfully applied %d updates:\n\n", len(updates))

	for _, u := range updates {
		if u.UpdateType == core.UpdateTypeNone {
			fmt.Fprintf(w, "  📌 %s: pinned to new digest\n", u.Label())

			continue
		}

		fmt.Fprintf(w, "  🚀 %s: %s -> %s (%s)\n", u.Label(), u.OldTag, u.NewTag, u.UpdateType)
	}
}
