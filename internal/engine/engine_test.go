package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/potibm/shiphoist/internal/core"
	"github.com/potibm/shiphoist/internal/ui"
)

const (
	testFilePath   = "docker-compose.yml"
	brokenFilePath = "broken-compose.yml"
	secondFilePath = "docker-compose.dev.yml"
	firstFilePath  = "docker-compose.prod.yml"
)

var errRegistry = errors.New("registry unavailable")

// stubDiscoverer returns a fixed set of updates.
type stubDiscoverer struct {
	updates []core.ImageUpdate
	err     error
	calls   atomic.Int32
}

func (d *stubDiscoverer) Discover(ctx context.Context, filePath string) ([]core.ImageUpdate, error) {
	d.calls.Add(1)

	return d.updates, d.err
}

// stubFetcher marks every image as selected unless it is listed as failing.
type stubFetcher struct {
	failing map[string]error

	calls atomic.Int32

	mu       sync.Mutex
	seenRepo []string
}

func (f *stubFetcher) FetchUpdate(ctx context.Context, current core.ImageUpdate) (core.ImageUpdate, error) {
	f.calls.Add(1)

	f.mu.Lock()
	f.seenRepo = append(f.seenRepo, current.ImageName)
	f.mu.Unlock()

	if err, ok := f.failing[current.ImageName]; ok {
		return core.ImageUpdate{}, err
	}

	updated := current
	updated.NewTag = current.OldTag + "-new"
	updated.NewDigest = "sha256:new"
	updated.UpdateType = core.UpdateTypePatch
	updated.Selected = true

	return updated, nil
}

// stubPrompter narrows the updates it is given, or fails outright.
type stubPrompter struct {
	err     error
	keep    map[string]bool
	calls   atomic.Int32
	invoked bool
}

func (p *stubPrompter) SelectUpdates(updates []core.ImageUpdate) ([]core.ImageUpdate, error) {
	p.calls.Add(1)
	p.invoked = true

	if p.err != nil {
		return nil, p.err
	}

	if p.keep == nil {
		return updates, nil
	}

	var kept []core.ImageUpdate

	for _, u := range updates {
		if p.keep[u.ServiceName] {
			kept = append(kept, u)
		}
	}

	return kept, nil
}

// stubPatcher records what it was asked to write.
type stubPatcher struct {
	err   error
	calls atomic.Int32

	mu      sync.Mutex
	applied []core.ImageUpdate
}

func (p *stubPatcher) Patch(ctx context.Context, filePath string, updates []core.ImageUpdate) error {
	p.calls.Add(1)

	if p.err != nil {
		return p.err
	}

	p.mu.Lock()
	p.applied = append(p.applied, updates...)
	p.mu.Unlock()

	return nil
}

// testUpdates returns three references in the primary file.
func testUpdates() []core.ImageUpdate {
	return []core.ImageUpdate{
		{FilePath: testFilePath, LineNumber: 4, ServiceName: "web", ImageName: "nginx", OldTag: "1.25.0"},
		{FilePath: testFilePath, LineNumber: 8, ServiceName: "db", ImageName: "postgres", OldTag: "16"},
		{FilePath: testFilePath, LineNumber: 12, ServiceName: "cache", ImageName: "redis", OldTag: "7.2"},
	}
}

// twoFileUpdates returns references spread over two files, with one repository
// shared between them.
func twoFileUpdates() []core.ImageUpdate {
	return []core.ImageUpdate{
		{FilePath: testFilePath, LineNumber: 4, ServiceName: "web", ImageName: "nginx", OldTag: "1.25.0"},
		{FilePath: testFilePath, LineNumber: 8, ServiceName: "db", ImageName: "postgres", OldTag: "16"},
		{FilePath: secondFilePath, LineNumber: 3, ServiceName: "api", ImageName: "nginx", OldTag: "1.25.0"},
		{FilePath: secondFilePath, LineNumber: 6, ServiceName: "jobs", ImageName: "redis", OldTag: "7.2"},
	}
}

// perFileDiscoverer hands out a discoverer whose references depend on the path
// it is given, which is what a real run relies on when the files differ in
// format.
func perFileDiscoverer(updatesByFile map[string][]core.ImageUpdate) func(string) core.Discoverer {
	return func(filePath string) core.Discoverer {
		return &stubDiscoverer{updates: updatesByFile[filePath]}
	}
}

// newTestPipeline builds a pipeline writing to a plain buffer. Output is not a
// terminal, so the progress bar is skipped and only the summary is written;
// tests that need per-image output set Verbose.
func newTestPipeline(
	discoverer *stubDiscoverer,
	fetcher core.RegistryFetcher,
	prompter *stubPrompter,
	patcher *stubPatcher,
) (*Pipeline, *bytes.Buffer) {
	out := &bytes.Buffer{}

	return &Pipeline{
		DiscovererFor: fixedDiscoverer(discoverer),
		Fetcher:       fetcher,
		Prompter:      prompter,
		Patcher:       patcher,
		Out:           out,
	}, out
}

func newVerboseTestPipeline(
	discoverer *stubDiscoverer,
	fetcher core.RegistryFetcher,
	prompter *stubPrompter,
	patcher *stubPatcher,
) (*Pipeline, *bytes.Buffer) {
	pipeline, out := newTestPipeline(discoverer, fetcher, prompter, patcher)
	pipeline.Verbose = true

	return pipeline, out
}

// runProcess runs the pipeline and returns the report, failing the test on an
// unexpected error. Tests about the error path call ProcessFile directly.
// fixedDiscoverer adapts one discoverer to the factory the pipeline expects.
//
// Most questions about the pipeline are asked over a single file, so the factory
// is a stand-in that hands out the same stub whatever path it is given. The
// tests that care about several files build their own.
func fixedDiscoverer(discoverer core.Discoverer) func(string) core.Discoverer {
	return func(string) core.Discoverer { return discoverer }
}

func runProcess(t *testing.T, pipeline *Pipeline) *core.Report {
	t.Helper()

	report, err := pipeline.ProcessFile(context.Background(), testFilePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	return report
}

func TestProcessFile_AppliesAllSelectedUpdates(t *testing.T) {
	discoverer := &stubDiscoverer{updates: testUpdates()}
	fetcher := &stubFetcher{}
	prompter := &stubPrompter{}
	patcher := &stubPatcher{}

	pipeline, _ := newTestPipeline(discoverer, fetcher, prompter, patcher)

	got := runProcess(t, pipeline).Updates

	if len(got) != 3 {
		t.Fatalf("expected 3 updates, got %d", len(got))
	}

	if patcher.calls.Load() != 1 {
		t.Errorf("expected the patcher to run once, ran %d times", patcher.calls.Load())
	}

	if len(patcher.applied) != 3 {
		t.Errorf("expected 3 updates to be patched, got %d", len(patcher.applied))
	}
}

func TestProcessFile_ProgressIsReportedForEveryImage(t *testing.T) {
	discoverer := &stubDiscoverer{updates: testUpdates()}
	fetcher := &stubFetcher{}
	prompter := &stubPrompter{}
	patcher := &stubPatcher{}

	pipeline, out := newVerboseTestPipeline(discoverer, fetcher, prompter, patcher)

	if _, err := pipeline.ProcessFile(context.Background(), testFilePath); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, service := range []string{"web", "db", "cache"} {
		if !strings.Contains(out.String(), "["+service+"]") {
			t.Errorf("expected progress output to mention service %q, got:\n%s", service, out.String())
		}
	}
}

func TestProcessFiles_UnreadableFileDoesNotStopTheOthers(t *testing.T) {
	readable := &stubDiscoverer{updates: testUpdates()}
	fetcher := &stubFetcher{}
	patcher := &stubPatcher{}

	pipeline, _ := newTestPipeline(readable, fetcher, &stubPrompter{}, patcher)
	pipeline.DiscovererFor = func(filePath string) core.Discoverer {
		if filePath == brokenFilePath {
			return &stubDiscoverer{err: errors.New("invalid yaml")}
		}

		return readable
	}

	report, err := pipeline.ProcessFiles(context.Background(), []string{brokenFilePath, testFilePath})
	if err != nil {
		t.Fatalf("expected the readable file to still be processed, got %v", err)
	}

	// The unreadable file is reported rather than raised, matching how a single
	// unreachable registry is handled: a stack with one stale file should still
	// get its other files updated.
	if len(report.Failures) != 1 {
		t.Fatalf("expected 1 failure, got %+v", report.Failures)
	}

	if !strings.Contains(report.Failures[0].Message, "discovery phase failed") {
		t.Errorf("expected a discovery-phase failure, got %q", report.Failures[0].Message)
	}

	if report.Failures[0].Image != brokenFilePath {
		t.Errorf("expected the failure to name the file, got %q", report.Failures[0].Image)
	}

	if len(report.Updates) != 3 {
		t.Errorf("expected the other file's 3 updates, got %d", len(report.Updates))
	}
}

// With nothing readable the run still succeeds and writes nothing, so a caller
// can tell "everything is up to date" from "nothing could be checked".
func TestProcessFiles_AllUnreadableReportsNoUpdates(t *testing.T) {
	broken := &stubDiscoverer{err: errors.New("invalid yaml")}
	fetcher := &stubFetcher{}
	patcher := &stubPatcher{}

	pipeline, _ := newTestPipeline(broken, fetcher, &stubPrompter{}, patcher)

	report, err := pipeline.ProcessFiles(context.Background(), []string{brokenFilePath})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Failures) != 1 {
		t.Errorf("expected 1 failure, got %+v", report.Failures)
	}

	if len(report.Updates) != 0 {
		t.Errorf("expected no updates, got %d", len(report.Updates))
	}

	if report.Written {
		t.Error("expected nothing to be written")
	}

	if patcher.calls.Load() != 0 {
		t.Error("expected no patching when nothing was readable")
	}
}

// A reference shared by two files is one registry lookup, not two. The grouping
// keys on the reference rather than on the declaration site, so the same
// image:tag in a Compose file and a Dockerfile costs one round-trip.
func TestProcessFiles_DeduplicatesAcrossFiles(t *testing.T) {
	updates := twoFileUpdates()
	fetcher := &stubFetcher{}
	patcher := &stubPatcher{}

	pipeline, out := newTestPipeline(nil, fetcher, &stubPrompter{}, patcher)
	pipeline.DiscovererFor = perFileDiscoverer(map[string][]core.ImageUpdate{
		testFilePath:   updates[:2],
		secondFilePath: updates[2:],
	})

	report, err := pipeline.ProcessFiles(context.Background(), []string{testFilePath, secondFilePath})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// nginx appears in both files, so three lookups cover four references.
	if report.Checked != 3 {
		t.Errorf("expected 3 lookups, got %d", report.Checked)
	}

	if report.References != 4 {
		t.Errorf("expected 4 references, got %d", report.References)
	}

	if fetcher.calls.Load() != 3 {
		t.Errorf("expected 3 registry calls, got %d", fetcher.calls.Load())
	}

	// The summary states the dedup, so a grouped run is not mistaken for a
	// truncated one.
	if !strings.Contains(out.String(), "3 unique images (4 references)") {
		t.Errorf("expected the shared lookup to be spelled out, got:\n%s", out.String())
	}
}

// Every reference the shared lookup covered is updated, each at its own line in
// its own file. Returning the representative alone would rewrite the first
// declaration twice and leave the second file untouched.
func TestProcessFiles_SharedFetchFansOutToEveryFile(t *testing.T) {
	updates := twoFileUpdates()
	fetcher := &stubFetcher{}
	patcher := &stubPatcher{}

	pipeline, _ := newTestPipeline(nil, fetcher, &stubPrompter{}, patcher)
	pipeline.DiscovererFor = perFileDiscoverer(map[string][]core.ImageUpdate{
		testFilePath:   updates[:2],
		secondFilePath: updates[2:],
	})

	report, err := pipeline.ProcessFiles(context.Background(), []string{testFilePath, secondFilePath})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Updates) != 4 {
		t.Fatalf("expected 4 updates, got %d", len(report.Updates))
	}

	if !report.Written {
		t.Error("expected the run to report a write")
	}

	if patcher.calls.Load() != 2 {
		t.Errorf("expected one write per file, got %d", patcher.calls.Load())
	}
}

// One selection for the whole run, not one per file. Asking twice would make a
// multi-file run confirm the same shared reference more than once.
func TestProcessFiles_PromptsOnceForTheWholeRun(t *testing.T) {
	updates := twoFileUpdates()
	fetcher := &stubFetcher{}
	prompter := &stubPrompter{}
	patcher := &stubPatcher{}

	pipeline, _ := newTestPipeline(nil, fetcher, prompter, patcher)
	pipeline.DiscovererFor = perFileDiscoverer(map[string][]core.ImageUpdate{
		testFilePath:   updates[:2],
		secondFilePath: updates[2:],
	})

	if _, err := pipeline.ProcessFiles(context.Background(), []string{testFilePath, secondFilePath}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if prompter.calls.Load() != 1 {
		t.Errorf("expected exactly one selection, got %d", prompter.calls.Load())
	}
}

// Each file is written with only its own updates. The patcher addresses lines by
// number within one file, so handing it the whole run would have it look up one
// file's line numbers in another.
func TestProcessFiles_PatchesEachFileWithItsOwnUpdates(t *testing.T) {
	updates := twoFileUpdates()
	fetcher := &stubFetcher{}
	patcher := &stubPatcher{}

	pipeline, _ := newTestPipeline(nil, fetcher, &stubPrompter{}, patcher)
	pipeline.DiscovererFor = perFileDiscoverer(map[string][]core.ImageUpdate{
		testFilePath:   updates[:2],
		secondFilePath: updates[2:],
	})

	if _, err := pipeline.ProcessFiles(context.Background(), []string{testFilePath, secondFilePath}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(patcher.applied) != 4 {
		t.Fatalf("expected 4 applied updates, got %d", len(patcher.applied))
	}

	for _, applied := range patcher.applied {
		// Every update carries the file it came from, so a mistake here would
		// show up as an update pointing at the wrong file.
		if applied.FilePath == "" {
			t.Error("expected every update to name its file")
		}
	}

	if !containsLine(patcher.applied, 4) {
		t.Error("expected the first file's line 4 to be patched")
	}

	if !containsLine(patcher.applied, 3) {
		t.Error("expected the second file's line 3 to be patched")
	}
}

// The report lists the files in command-line order, so a consumer can tell what
// a run covered without reading the updates.
func TestProcessFiles_ReportListsEveryFile(t *testing.T) {
	updates := twoFileUpdates()
	fetcher := &stubFetcher{}
	patcher := &stubPatcher{}

	pipeline, _ := newTestPipeline(nil, fetcher, &stubPrompter{}, patcher)
	pipeline.DiscovererFor = perFileDiscoverer(map[string][]core.ImageUpdate{
		testFilePath:   updates[:2],
		secondFilePath: updates[2:],
	})

	report, err := pipeline.ProcessFiles(context.Background(), []string{firstFilePath, testFilePath, secondFilePath})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !slices.Equal(report.Files, []string{firstFilePath, testFilePath, secondFilePath}) {
		t.Errorf("expected the files in command-line order, got %v", report.Files)
	}
}

// A file with nothing in it is not written, so an unchanged file keeps its
// mtime and permissions rather than being rewritten identically.
func TestProcessFiles_FileWithoutUpdatesIsNotWritten(t *testing.T) {
	updates := testUpdates()
	fetcher := &stubFetcher{}
	patcher := &stubPatcher{}

	pipeline, _ := newTestPipeline(nil, fetcher, &stubPrompter{}, patcher)
	pipeline.DiscovererFor = perFileDiscoverer(map[string][]core.ImageUpdate{
		testFilePath:   updates,
		secondFilePath: nil,
	})

	report, err := pipeline.ProcessFiles(context.Background(), []string{testFilePath, secondFilePath})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if patcher.calls.Load() != 1 {
		t.Errorf("expected exactly one file to be written, got %d", patcher.calls.Load())
	}

	if report.References != 3 {
		t.Errorf("expected 3 references, got %d", report.References)
	}
}

func TestProcessFiles_NoPaths(t *testing.T) {
	fetcher := &stubFetcher{}
	patcher := &stubPatcher{}

	pipeline, _ := newTestPipeline(&stubDiscoverer{}, fetcher, &stubPrompter{}, patcher)

	report, err := pipeline.ProcessFiles(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Updates) != 0 || report.Written || len(report.Files) != 0 {
		t.Errorf("expected an empty report, got %+v", report)
	}
}

// The pipeline needs a discoverer per file, and a nil factory is a wiring
// mistake rather than a run that covers nothing.
func TestProcessFiles_WithoutADiscovererReportsEveryFile(t *testing.T) {
	fetcher := &stubFetcher{}
	patcher := &stubPatcher{}

	pipeline := &Pipeline{Fetcher: fetcher, Patcher: patcher, Out: &bytes.Buffer{}}

	report, err := pipeline.ProcessFiles(context.Background(), []string{testFilePath})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Failures) != 1 {
		t.Errorf("expected the file to be reported as failed, got %+v", report.Failures)
	}

	if patcher.calls.Load() != 0 {
		t.Error("expected nothing to be written")
	}
}

func containsLine(updates []core.ImageUpdate, line int) bool {
	for _, u := range updates {
		if u.LineNumber == line {
			return true
		}
	}

	return false
}

func TestProcessFile_NoImagesDiscovered(t *testing.T) {
	discoverer := &stubDiscoverer{updates: nil}
	fetcher := &stubFetcher{}
	prompter := &stubPrompter{}
	patcher := &stubPatcher{}

	pipeline, _ := newTestPipeline(discoverer, fetcher, prompter, patcher)

	got := runProcess(t, pipeline).Updates

	if len(got) != 0 {
		t.Errorf("expected no updates, got %d", len(got))
	}

	if prompter.invoked {
		t.Error("expected the prompter to be skipped when nothing was discovered")
	}

	if patcher.calls.Load() != 0 {
		t.Error("expected no patching when nothing was discovered")
	}
}

func TestProcessFile_AllFetchesFail(t *testing.T) {
	discoverer := &stubDiscoverer{updates: testUpdates()}
	fetcher := &stubFetcher{failing: map[string]error{
		"nginx":    errRegistry,
		"postgres": errRegistry,
		"redis":    errRegistry,
	}}
	prompter := &stubPrompter{}
	patcher := &stubPatcher{}

	pipeline, out := newTestPipeline(discoverer, fetcher, prompter, patcher)

	report, err := pipeline.ProcessFile(context.Background(), testFilePath)
	if err != nil {
		t.Fatalf("a per-image registry error must not fail the run, got %v", err)
	}

	got := report.Updates

	if len(got) != 0 {
		t.Errorf("expected no updates, got %d", len(got))
	}

	if !strings.Contains(out.String(), "Skipped 3 images") {
		t.Errorf("expected a skipped-images block, got:\n%s", out.String())
	}

	if !strings.Contains(out.String(), errRegistry.Error()) {
		t.Errorf("expected the underlying error to be reported, got:\n%s", out.String())
	}

	if !strings.Contains(out.String(), "3 skipped") {
		t.Errorf("expected the summary to count the skips, got:\n%s", out.String())
	}

	if patcher.calls.Load() != 0 {
		t.Error("expected no patching when every fetch failed")
	}
}

func TestProcessFile_PartialFetchFailureStillPatches(t *testing.T) {
	discoverer := &stubDiscoverer{updates: testUpdates()}
	fetcher := &stubFetcher{failing: map[string]error{"postgres": errRegistry}}
	prompter := &stubPrompter{}
	patcher := &stubPatcher{}

	pipeline, _ := newTestPipeline(discoverer, fetcher, prompter, patcher)

	got := runProcess(t, pipeline).Updates

	if len(got) != 2 {
		t.Fatalf("expected the 2 healthy images to survive, got %d", len(got))
	}

	for _, u := range got {
		if u.ImageName == "postgres" {
			t.Error("expected the failing image to be excluded")
		}
	}
}

func TestProcessFile_UnselectedUpdatesAreDropped(t *testing.T) {
	discoverer := &stubDiscoverer{updates: testUpdates()}

	// A fetcher that finds nothing new marks nothing as selected.
	fetcher := &nothingSelectedFetcher{}
	prompter := &stubPrompter{}
	patcher := &stubPatcher{}

	pipeline, _ := newTestPipeline(discoverer, fetcher, prompter, patcher)

	got := runProcess(t, pipeline).Updates

	if len(got) != 0 {
		t.Errorf("expected no updates, got %d", len(got))
	}

	if prompter.invoked {
		t.Error("expected the prompter to be skipped when nothing is selected")
	}

	if patcher.calls.Load() != 0 {
		t.Error("expected no patching when nothing is selected")
	}
}

// nothingSelectedFetcher simulates an up-to-date image.
type nothingSelectedFetcher struct{}

func (nothingSelectedFetcher) FetchUpdate(ctx context.Context, current core.ImageUpdate) (core.ImageUpdate, error) {
	current.Selected = false
	current.UpdateType = core.UpdateTypeNone

	return current, nil
}

func TestProcessFile_PrompterNarrowsSelection(t *testing.T) {
	discoverer := &stubDiscoverer{updates: testUpdates()}
	fetcher := &stubFetcher{}
	prompter := &stubPrompter{keep: map[string]bool{"web": true}}
	patcher := &stubPatcher{}

	pipeline, _ := newTestPipeline(discoverer, fetcher, prompter, patcher)

	got := runProcess(t, pipeline).Updates

	if len(got) != 1 {
		t.Fatalf("expected 1 update, got %d", len(got))
	}

	if got[0].ServiceName != "web" {
		t.Errorf("expected only the web service, got %q", got[0].ServiceName)
	}

	if len(patcher.applied) != 1 {
		t.Errorf("expected 1 patched update, got %d", len(patcher.applied))
	}
}

func TestProcessFile_PrompterRejectsEverything(t *testing.T) {
	discoverer := &stubDiscoverer{updates: testUpdates()}
	fetcher := &stubFetcher{}
	prompter := &stubPrompter{keep: map[string]bool{}}
	patcher := &stubPatcher{}

	pipeline, _ := newTestPipeline(discoverer, fetcher, prompter, patcher)

	got := runProcess(t, pipeline).Updates

	if len(got) != 0 {
		t.Errorf("expected no updates, got %d", len(got))
	}

	if patcher.calls.Load() != 0 {
		t.Error("expected no patching when the user deselected everything")
	}
}

func TestProcessFile_PrompterErrorAborts(t *testing.T) {
	discoverer := &stubDiscoverer{updates: testUpdates()}
	fetcher := &stubFetcher{}
	prompter := &stubPrompter{err: errors.New("tty unavailable")}
	patcher := &stubPatcher{}

	pipeline, _ := newTestPipeline(discoverer, fetcher, prompter, patcher)

	if _, err := pipeline.ProcessFile(context.Background(), testFilePath); err == nil {
		t.Fatal("expected an error")
	}

	if patcher.calls.Load() != 0 {
		t.Error("expected no patching after the prompter failed")
	}
}

// A nil Prompter means non-interactive operation: everything selected is
// applied directly.
func TestProcessFile_NilPrompterAppliesEverything(t *testing.T) {
	discoverer := &stubDiscoverer{updates: testUpdates()}
	fetcher := &stubFetcher{}
	patcher := &stubPatcher{}

	pipeline := &Pipeline{
		DiscovererFor: fixedDiscoverer(discoverer),
		Fetcher:       fetcher,
		Patcher:       patcher,
		Out:           &bytes.Buffer{},
	}

	got := runProcess(t, pipeline).Updates

	if len(got) != 3 {
		t.Errorf("expected all 3 updates to be applied, got %d", len(got))
	}
}

func TestProcessFile_PatcherErrorIsWrapped(t *testing.T) {
	discoverer := &stubDiscoverer{updates: testUpdates()}
	fetcher := &stubFetcher{}
	prompter := &stubPrompter{}
	patcher := &stubPatcher{err: errors.New("read-only file system")}

	pipeline, _ := newTestPipeline(discoverer, fetcher, prompter, patcher)

	_, err := pipeline.ProcessFile(context.Background(), testFilePath)
	if err == nil {
		t.Fatal("expected an error")
	}

	if !strings.Contains(err.Error(), "failed to patch file") {
		t.Errorf("expected a wrapped patch error, got %v", err)
	}
}

func TestProcessFile_CancelledContext(t *testing.T) {
	discoverer := &stubDiscoverer{updates: testUpdates()}
	fetcher := &stubFetcher{}
	prompter := &stubPrompter{}
	patcher := &stubPatcher{}

	pipeline, _ := newTestPipeline(discoverer, fetcher, prompter, patcher)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// A cancelled parent context must not cause a panic or a hang; the
	// per-image fetches simply observe the cancellation.
	_, err := pipeline.ProcessFile(ctx, testFilePath)
	if err != nil {
		t.Logf("run reported: %v", err)
	}
}

// The fan-out runs one goroutine per image and appends under a mutex. Run with
// -race to catch regressions in that handoff.
func TestProcessFile_ConcurrentFetchesAreRaceFree(t *testing.T) {
	const imageCount = 50

	updates := make([]core.ImageUpdate, 0, imageCount)
	for i := range imageCount {
		updates = append(updates, core.ImageUpdate{
			FilePath:    testFilePath,
			LineNumber:  i + 1,
			ServiceName: "svc",
			ImageName:   fmt.Sprintf("img-%d", i),
			OldTag:      "1.0.0",
		})
	}

	discoverer := &stubDiscoverer{updates: updates}
	fetcher := &stubFetcher{}
	prompter := &stubPrompter{}
	patcher := &stubPatcher{}

	pipeline, out := newTestPipeline(discoverer, fetcher, prompter, patcher)

	got := runProcess(t, pipeline).Updates

	if len(got) != imageCount {
		t.Errorf("expected %d updates, got %d", imageCount, len(got))
	}

	if fetcher.calls.Load() != imageCount {
		t.Errorf("expected %d fetches, got %d", imageCount, fetcher.calls.Load())
	}

	if lines := strings.Count(out.String(), "Checked"); lines != 1 {
		t.Errorf("expected exactly 1 summary line, got %d:\n%s", lines, out.String())
	}
}

// The progress counter must be exact even though it is incremented from many
// goroutines.
func TestProcessFile_ProgressCounterCoversEveryImage(t *testing.T) {
	discoverer := &stubDiscoverer{updates: testUpdates()}
	fetcher := &stubFetcher{}
	prompter := &stubPrompter{}
	patcher := &stubPatcher{}

	pipeline, out := newVerboseTestPipeline(discoverer, fetcher, prompter, patcher)

	if _, err := pipeline.ProcessFile(context.Background(), testFilePath); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for i := 1; i <= 3; i++ {
		if !strings.Contains(out.String(), "["+strconv.Itoa(i)+"/3]") {
			t.Errorf("expected a [%d/3] progress marker, got:\n%s", i, out.String())
		}
	}
}

func TestPipeline_OutputDefaultsToStdout(t *testing.T) {
	pipeline := &Pipeline{}

	if pipeline.output() == nil {
		t.Error("expected a non-nil default output writer")
	}
}

func TestProcessFile_ReportDescribesTheRun(t *testing.T) {
	discoverer := &stubDiscoverer{updates: testUpdates()}
	fetcher := &stubFetcher{failing: map[string]error{"postgres": errRegistry}}
	pipeline, _ := newTestPipeline(discoverer, fetcher, &stubPrompter{}, &stubPatcher{})

	report := runProcess(t, pipeline)

	if !slices.Equal(report.Files, []string{testFilePath}) {
		t.Errorf("expected the file to be recorded, got %v", report.Files)
	}

	if report.References != 3 {
		t.Errorf("expected 3 references, got %d", report.References)
	}

	if report.Checked != 3 {
		t.Errorf("expected 3 lookups, got %d", report.Checked)
	}

	if !report.Written {
		t.Error("expected a real run that patched the file to report written")
	}

	if report.DryRun {
		t.Error("expected a real run not to report dry_run")
	}

	if len(report.Updates) != 2 {
		t.Errorf("expected 2 applied updates, got %d", len(report.Updates))
	}
}

// A failed image is reported, not dropped: a CI job has to be able to see that
// something was skipped and why.
func TestProcessFile_ReportCarriesFailures(t *testing.T) {
	discoverer := &stubDiscoverer{updates: testUpdates()}
	fetcher := &stubFetcher{failing: map[string]error{"postgres": errRegistry}}
	pipeline, _ := newTestPipeline(discoverer, fetcher, &stubPrompter{}, &stubPatcher{})

	report := runProcess(t, pipeline)

	if len(report.Failures) != 1 {
		t.Fatalf("expected 1 failure, got %+v", report.Failures)
	}

	failure := report.Failures[0]
	if !strings.Contains(failure.Image, "postgres") {
		t.Errorf("expected the failing image to be named, got %q", failure.Image)
	}

	if failure.Message != errRegistry.Error() {
		t.Errorf("expected the cause to be reported, got %q", failure.Message)
	}

	// A partial failure is still a run that happened, not an error.
	if !report.Written {
		t.Error("expected the surviving updates to have been written")
	}
}

func TestProcessFile_ReportOnAnEmptyFile(t *testing.T) {
	pipeline, _ := newTestPipeline(&stubDiscoverer{}, &stubFetcher{}, &stubPrompter{}, &stubPatcher{})

	report := runProcess(t, pipeline)

	if report.References != 0 || report.Checked != 0 || report.Written {
		t.Errorf("expected an empty, unwritten report, got %+v", report)
	}

	// The report must still be a well-formed document, since a run that found
	// nothing is exactly when a CI job reads the JSON.
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}

	for _, want := range []string{`"updates":[]`, `"failures":[]`} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("expected %s in:\n%s", want, encoded)
		}
	}
}

// A dry run must report exactly what a real run would have applied, without
// touching the file.
func TestProcessFile_DryRunReportsWithoutPatching(t *testing.T) {
	discoverer := &stubDiscoverer{updates: testUpdates()}
	patcher := &stubPatcher{}
	pipeline, _ := newTestPipeline(discoverer, &stubFetcher{}, &stubPrompter{}, patcher)
	pipeline.DryRun = true

	report := runProcess(t, pipeline)

	if patcher.calls.Load() != 0 {
		t.Errorf("expected no patching in a dry run, ran %d times", patcher.calls.Load())
	}

	if report.Written {
		t.Error("expected a dry run not to report written")
	}

	if !report.DryRun {
		t.Error("expected a dry run to report dry_run")
	}

	if len(report.Updates) != 3 {
		t.Errorf("expected the dry run to report the 3 updates it would apply, got %d", len(report.Updates))
	}
}

// Nothing selected and a dry run are different outcomes, and the report has to
// distinguish them.
func TestProcessFile_DryRunWithNothingSelected(t *testing.T) {
	patcher := &stubPatcher{}
	pipeline, _ := newTestPipeline(
		&stubDiscoverer{updates: testUpdates()},
		&stubFetcher{},
		&stubPrompter{keep: map[string]bool{}},
		patcher,
	)
	pipeline.DryRun = true

	report := runProcess(t, pipeline)

	if !report.DryRun || report.Written {
		t.Errorf("expected dry_run without written, got %+v", report)
	}

	if len(report.Updates) != 0 {
		t.Errorf("expected no updates, got %d", len(report.Updates))
	}
}

func TestCoreFailures(t *testing.T) {
	tests := []struct {
		name  string
		input []ui.Failure
		want  []core.Failure
	}{
		{
			name:  "no failures",
			input: nil,
			want:  []core.Failure{},
		},
		{
			name:  "label and cause are carried over",
			input: []ui.Failure{{Label: "[db] postgres", Err: errRegistry}},
			want: []core.Failure{
				{Image: "[db] postgres", Message: errRegistry.Error()},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := coreFailures(tt.input)

			if !slices.Equal(got, tt.want) {
				t.Errorf("expected %+v, got %+v", tt.want, got)
			}

			// The result is serialised, so it must never be nil.
			if got == nil {
				t.Error("expected an initialised slice")
			}
		})
	}
}
