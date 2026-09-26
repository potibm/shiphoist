package engine

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/potibm/shiphoist/internal/core"
	"github.com/potibm/shiphoist/internal/ui"
)

const imageProcessingTimeout = 60

// Pipeline orchestrates the entire update lifecycle.
// It ties together discovery, remote fetching, and local patching.
type Pipeline struct {
	Discoverer core.Discoverer
	Fetcher    core.RegistryFetcher
	Patcher    core.Patcher
	Prompter   core.Prompter

	// Out receives progress and summary output. Defaults to os.Stdout.
	Out io.Writer

	// Verbose reports one line per image instead of a single updating
	// progress bar.
	Verbose bool
}

// ProcessFile runs the discovered images through the update pipeline and
// returns the updates that were applied.
//
// References that resolve to the same registry answer are fetched once and the
// outcome shared, so a Compose file reusing one image across many services
// costs a single round-trip.
func (p *Pipeline) ProcessFile(ctx context.Context, filePath string) ([]core.ImageUpdate, error) {
	updates, err := p.Discoverer.Discover(ctx, filePath)
	if err != nil {
		return nil, fmt.Errorf("discovery phase failed for %s: %w", filePath, err)
	}

	totalUpdates := len(updates)
	if totalUpdates == 0 {
		return nil, nil
	}

	started := time.Now()
	groups := groupUpdates(updates)
	progress := p.newProgress(len(groups))

	fetched := p.fetchAll(ctx, groups, progress)

	summary := progressSummary(len(groups), totalUpdates, time.Since(started), len(progress.Failures()))
	progress.Stop(summary)

	if len(fetched) == 0 {
		return nil, nil
	}

	selectedUpdates := fetched
	if p.Prompter != nil {
		selectedUpdates, err = p.Prompter.SelectUpdates(fetched)
		if err != nil {
			return nil, err
		}
	}

	if len(selectedUpdates) == 0 {
		return nil, nil
	}

	if err := p.Patcher.Patch(ctx, filePath, selectedUpdates); err != nil {
		return nil, fmt.Errorf("failed to patch file %s: %w", filePath, err)
	}

	return selectedUpdates, nil
}

// output returns the configured output sink, defaulting to stdout.
func (p *Pipeline) output() io.Writer {
	if p.Out == nil {
		return os.Stdout
	}

	return p.Out
}

// newProgress builds a reporter sized to the number of distinct lookups.
//
// A plain io.Writer such as a test buffer is not a terminal, so the bar is
// skipped automatically and only the summary is written.
func (p *Pipeline) newProgress(total int) *ui.Progress {
	out := p.output()

	file, isFile := out.(*os.File)

	return ui.NewProgress(out, total, ui.ProgressOptions{
		Interactive: isFile && ui.IsTerminal(file),
		Verbose:     p.Verbose,
		Width:       ui.TerminalWidth(file),
	})
}

// progressSummary describes what the run did. Deduplication is made explicit so
// a grouped run is not mistaken for a truncated one.
func progressSummary(groups, references int, elapsed time.Duration, skipped int) string {
	subject := fmt.Sprintf("%d images", groups)
	if groups != references {
		subject = fmt.Sprintf("%d unique %s (%d references)",
			groups, plural(groups, "image", "images"), references)
	}

	summary := fmt.Sprintf("✅ Checked %s in %s", subject, ui.FormatDuration(elapsed))
	if skipped > 0 {
		summary += fmt.Sprintf(" · %d skipped", skipped)
	}

	return summary
}

func plural(n int, singular, many string) string {
	if n == 1 {
		return singular
	}

	return many
}

// fetchAll resolves every group concurrently and returns the selected updates
// in file order. Individual failures are reported and skipped rather than
// aborting the run.
func (p *Pipeline) fetchAll(
	ctx context.Context,
	groups []fetchGroup,
	progress *ui.Progress,
) []core.ImageUpdate {
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		resolved = make([]core.ImageUpdate, 0, len(groups))
	)

	for _, group := range groups {
		wg.Add(1)

		go func(g fetchGroup) {
			defer wg.Done()

			applied := p.fetchGroup(ctx, g, progress)
			if len(applied) == 0 {
				return
			}

			mu.Lock()

			resolved = append(resolved, applied...)

			mu.Unlock()
		}(group)
	}

	wg.Wait()

	sortByDeclarationOrder(resolved)

	return resolved
}

// fetchGroup resolves one distinct lookup and shares the outcome across every
// reference that asked for it. It bounds the work with its own timeout so a
// single slow registry cannot stall the run.
func (p *Pipeline) fetchGroup(
	ctx context.Context,
	group fetchGroup,
	progress *ui.Progress,
) []core.ImageUpdate {
	imgCtx, cancel := context.WithTimeout(ctx, imageProcessingTimeout*time.Second)
	defer cancel()

	result, err := p.Fetcher.FetchUpdate(imgCtx, group.Representative)
	if err != nil {
		progress.Fail(group.label(), err)

		return nil
	}

	progress.Advance(group.label())

	if !result.Selected {
		return nil
	}

	return applyResultToAll(result, group.Members)
}

// sortByDeclarationOrder puts updates back into file order. Concurrent lookups
// finish in arbitrary order, so without this the TUI and the summary would
// shuffle between runs over identical input.
func sortByDeclarationOrder(updates []core.ImageUpdate) {
	sort.SliceStable(updates, func(i, j int) bool {
		if updates[i].FilePath != updates[j].FilePath {
			return updates[i].FilePath < updates[j].FilePath
		}

		return updates[i].LineNumber < updates[j].LineNumber
	})
}
