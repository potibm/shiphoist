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

	// DryRun resolves and reports the updates without writing to the file.
	// The reported updates are exactly what a real run would have applied.
	DryRun bool

	// Quiet suppresses the progress bar, the summary and the skipped-images
	// block. The report is unaffected.
	Quiet bool
}

// ProcessFile runs the discovered images through the update pipeline and
// returns a report of what happened.
//
// References that resolve to the same registry answer are fetched once and the
// outcome shared, so a Compose file reusing one image across many services
// costs a single round-trip.
//
// A report is returned whenever the run itself succeeded, including when
// individual images failed: those are listed in Report.Failures. An error means
// the run could not be completed, and no report is returned.
func (p *Pipeline) ProcessFile(ctx context.Context, filePath string) (*core.Report, error) {
	updates, err := p.Discoverer.Discover(ctx, filePath)
	if err != nil {
		return nil, fmt.Errorf("discovery phase failed for %s: %w", filePath, err)
	}

	report := newReport(filePath, len(updates))
	report.DryRun = p.DryRun

	if len(updates) == 0 {
		return report, nil
	}

	started := time.Now()
	groups := groupUpdates(updates)
	progress := p.newProgress(len(groups))

	fetched := p.fetchAll(ctx, groups, progress)

	// The failure count has to be known before Stop, which erases the line and
	// prints the summary that quotes it.
	failures := progress.Failures()
	elapsed := time.Since(started)
	progress.Stop(progressSummary(len(groups), report.References, elapsed, len(failures)))

	report.Checked = len(groups)
	report.ElapsedMS = elapsed.Milliseconds()
	report.Failures = coreFailures(failures)

	if len(fetched) == 0 {
		return report, nil
	}

	selectedUpdates := fetched
	if p.Prompter != nil {
		selectedUpdates, err = p.Prompter.SelectUpdates(fetched)
		if err != nil {
			return nil, err
		}
	}

	if len(selectedUpdates) == 0 {
		return report, nil
	}

	// A dry run reports the selection but never touches the file.
	if p.DryRun {
		report.Updates = selectedUpdates

		return report, nil
	}

	if err := p.Patcher.Patch(ctx, filePath, selectedUpdates); err != nil {
		return nil, fmt.Errorf("failed to patch file %s: %w", filePath, err)
	}

	report.Updates = selectedUpdates
	report.Written = true

	return report, nil
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
		Quiet:       p.Quiet,
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
