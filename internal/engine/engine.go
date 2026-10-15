package engine

import (
	"context"
	"errors"
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
	// DiscovererFor returns the discoverer for one file.
	//
	// It is a factory rather than a single discoverer because a run can cover
	// several files in several formats: a Compose file and a Dockerfile in the
	// same invocation are read by different discoverers, and the format follows
	// from the file name. A single instance could serve at most one format.
	DiscovererFor func(filePath string) core.Discoverer

	Fetcher  core.RegistryFetcher
	Patcher  core.Patcher
	Prompter core.Prompter

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

// ProcessFile runs the pipeline over a single file. It is ProcessFiles with
// one path, kept because a run over one file is the common case.
func (p *Pipeline) ProcessFile(ctx context.Context, filePath string) (*core.Report, error) {
	return p.ProcessFiles(ctx, []string{filePath})
}

// ProcessFiles runs every file's discovered images through the update pipeline
// and returns a single report of what happened.
//
// The phases span the whole run rather than repeating per file, which is what
// makes a multi-file run behave like one run: references are deduplicated
// across files, the user is asked once, and each file is patched only with its
// own updates.
//
// A failure to read one file is reported in the report and does not stop the
// others, matching how a single unreachable registry is handled: a stack with
// one stale file should still get its other files updated.
//
// A report is returned whenever the run itself succeeded, including when
// individual images failed: those are listed in Report.Failures. An error means
// the run could not be completed, and no report is returned.
func (p *Pipeline) ProcessFiles(ctx context.Context, paths []string) (*core.Report, error) {
	report, updates, err := p.discoverAll(ctx, paths)
	if err != nil {
		return nil, err
	}

	if len(updates) == 0 {
		return report, nil
	}

	report.DryRun = p.DryRun

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
	// Appended rather than assigned: discovery already recorded the files it
	// could not read, and overwriting them would drop them from the report.
	report.Failures = append(report.Failures, coreFailures(failures)...)

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

	// A dry run reports the selection but never touches the files.
	if p.DryRun {
		report.Updates = selectedUpdates

		return report, nil
	}

	if err := p.patchByFile(ctx, selectedUpdates); err != nil {
		return nil, err
	}

	report.Updates = selectedUpdates
	report.Written = true

	return report, nil
}

// discoverAll reads every file and returns the references across all of them,
// in the order the paths were given.
//
// A file that cannot be read becomes a failure rather than an error, so one
// bad path does not cost the run the files that were readable.
func (p *Pipeline) discoverAll(ctx context.Context, paths []string) (*core.Report, []core.ImageUpdate, error) {
	report := newReport(paths, 0)

	var updates []core.ImageUpdate

	for _, path := range paths {
		found, err := p.discoverFile(ctx, path)
		if err != nil {
			report.Failures = append(report.Failures, core.Failure{Image: path, Message: err.Error()})

			continue
		}

		updates = append(updates, found...)
		report.References += len(found)
	}

	return report, updates, nil
}

// discoverFile resolves one file, recording a read failure as a report failure
// rather than an error. The report already names the file, so the path is the
// label the failure is filed under.
func (p *Pipeline) discoverFile(ctx context.Context, filePath string) ([]core.ImageUpdate, error) {
	if p.DiscovererFor == nil {
		return nil, errors.New("no discoverer configured")
	}

	updates, err := p.DiscovererFor(filePath).Discover(ctx, filePath)
	if err != nil {
		return nil, fmt.Errorf("discovery phase failed for %s: %w", filePath, err)
	}

	return updates, nil
}

// patchByFile writes each file with only its own updates.
//
// The patcher addresses lines by number within one file, so handing it the
// updates of the whole run would have it look up file A's line numbers in file
// B. Grouping first also means a file with nothing selected is never opened for
// writing, so an unchanged file keeps its mtime and permissions.
func (p *Pipeline) patchByFile(ctx context.Context, updates []core.ImageUpdate) error {
	for filePath, fileUpdates := range groupByFile(updates) {
		if err := p.Patcher.Patch(ctx, filePath, fileUpdates); err != nil {
			return fmt.Errorf("failed to patch file %s: %w", filePath, err)
		}
	}

	return nil
}

// groupByFile buckets updates by the file they belong to, keeping the order
// they arrived in within each bucket.
func groupByFile(updates []core.ImageUpdate) map[string][]core.ImageUpdate {
	byFile := make(map[string][]core.ImageUpdate, 1)

	for _, update := range updates {
		byFile[update.FilePath] = append(byFile[update.FilePath], update)
	}

	return byFile
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
