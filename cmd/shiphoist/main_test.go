package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/potibm/shiphoist/internal/core"
	"github.com/potibm/shiphoist/internal/discovery"
)

const composeFixture = `services:
  db:
    image: postgres:16.2
  cache:
    image: redis:7.2.1
`

// stubFetcher resolves every reference with resolve, unless the image is listed
// in failing or err rejects everything.
type stubFetcher struct {
	resolve func(core.ImageUpdate) core.ImageUpdate
	failing map[string]error
	err     error
}

func (s stubFetcher) FetchUpdate(_ context.Context, current core.ImageUpdate) (core.ImageUpdate, error) {
	if s.err != nil {
		return current, s.err
	}

	if err, ok := s.failing[current.ImageName]; ok {
		return current, err
	}

	if s.resolve == nil {
		return current, nil
	}

	return s.resolve(current), nil
}

// upgrade resolves to a selected minor bump with a digest, which is the shape
// the patcher needs to write a change.
func upgrade(current core.ImageUpdate) core.ImageUpdate {
	current.NewTag = current.OldTag + ".1"
	current.NewDigest = "sha256:newdigest"
	current.UpdateType = core.UpdateTypeMinor
	current.Selected = true

	return current
}

// acceptAllPrompter stands in for a user who confirmed the preselected
// defaults.
type acceptAllPrompter struct{}

func (acceptAllPrompter) SelectUpdates(updates []core.ImageUpdate) ([]core.ImageUpdate, error) {
	return updates, nil
}

// rejectAllPrompter stands in for a user who selected nothing.
type rejectAllPrompter struct{}

func (rejectAllPrompter) SelectUpdates([]core.ImageUpdate) ([]core.ImageUpdate, error) {
	return nil, nil
}

// failingPrompter stands in for an aborted or broken form.
type failingPrompter struct {
	err error
}

func (p failingPrompter) SelectUpdates([]core.ImageUpdate) ([]core.ImageUpdate, error) {
	return nil, p.err
}

// harness collects the output sinks and lets a test override one dependency.
type harness struct {
	deps    deps
	out     *bytes.Buffer
	errOut  *bytes.Buffer
	compose string
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	var (
		out    bytes.Buffer
		errOut bytes.Buffer
	)

	path := filepath.Join(t.TempDir(), "docker-compose.yml")
	if err := os.WriteFile(path, []byte(composeFixture), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	return &harness{
		deps: deps{
			Out:    &out,
			ErrOut: &errOut,
			NewFetcher: func(bool) (core.RegistryFetcher, error) {
				return stubFetcher{resolve: upgrade}, nil
			},
			NewPrompter:   func(core.UpdateType) core.Prompter { return acceptAllPrompter{} },
			IsInteractive: func() bool { return true },
		},
		out:     &out,
		errOut:  &errOut,
		compose: path,
	}
}

func TestRunUpdate_PatchesFileAndSummarises(t *testing.T) {
	h := newHarness(t)

	if err := runUpdate(h.deps, options{}, h.compose); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	written := readFile(t, h.compose)

	for _, want := range []string{
		"postgres:16.2.1@sha256:newdigest",
		"redis:7.2.1.1@sha256:newdigest",
	} {
		if !strings.Contains(written, want) {
			t.Errorf("expected %q in the patched file:\n%s", want, written)
		}
	}

	assertContains(t, h.out.String(), "Successfully applied 2 updates")
	assertContains(t, h.out.String(), "[db] postgres")
	assertContains(t, h.out.String(), "[cache] redis")
}

// The progress summary is written to the injected sink, not to os.Stdout, so a
// later machine-readable mode can own stdout outright.
func TestRunUpdate_ProgressGoesToInjectedWriter(t *testing.T) {
	h := newHarness(t)

	if err := runUpdate(h.deps, options{}, h.compose); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertContains(t, h.out.String(), "Checked 2 images")
}

func TestRunUpdate_NothingSelectedLeavesFileUntouched(t *testing.T) {
	h := newHarness(t)
	h.deps.NewPrompter = func(core.UpdateType) core.Prompter { return rejectAllPrompter{} }

	if err := runUpdate(h.deps, options{}, h.compose); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if written := readFile(t, h.compose); written != composeFixture {
		t.Errorf("expected the file to be untouched:\n%s", written)
	}

	assertContains(t, h.out.String(), "Everything is up to date")
}

// A nil prompter is the non-interactive path: apply everything the registry
// resolved, with no question asked.
func TestRunUpdate_NilPrompterAppliesEverything(t *testing.T) {
	h := newHarness(t)
	h.deps.NewPrompter = func(core.UpdateType) core.Prompter { return nil }

	if err := runUpdate(h.deps, options{}, h.compose); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertContains(t, h.out.String(), "Successfully applied 2 updates")
}

func TestRunUpdate_ForceRefreshIsAnnounced(t *testing.T) {
	h := newHarness(t)

	if err := runUpdate(h.deps, options{ForceRefresh: true}, h.compose); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertContains(t, h.out.String(), "Force refresh activated")
}

func TestRunUpdate_VerboseReachesThePipeline(t *testing.T) {
	h := newHarness(t)

	if err := runUpdate(h.deps, options{Verbose: true}, h.compose); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verbose replaces the single summary line with one line per image.
	if strings.Count(h.out.String(), "✅ [1/2]") != 1 {
		t.Errorf("expected a per-image line, got:\n%s", h.out.String())
	}
}

func TestRunUpdate_Errors(t *testing.T) {
	unknownImage := filepath.Join(t.TempDir(), "missing.yml")

	tests := []struct {
		name     string
		deps     func(h *harness)
		filePath func(h *harness) string
		wantErr  string
	}{
		{
			name:     "fetcher cannot be built",
			deps:     func(h *harness) { h.deps.NewFetcher = failingFetcher },
			filePath: func(h *harness) string { return h.compose },
			wantErr:  "failed to initialize fetcher",
		},
		{
			name:     "file does not exist",
			deps:     func(*harness) {},
			filePath: func(*harness) string { return unknownImage },
			wantErr:  "discovery phase failed",
		},
		{
			name: "prompter fails",
			deps: func(h *harness) {
				h.deps.NewPrompter = func(core.UpdateType) core.Prompter {
					return failingPrompter{err: errors.New("user aborted")}
				}
			},
			filePath: func(h *harness) string { return h.compose },
			wantErr:  "user aborted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			tt.deps(h)

			err := runUpdate(h.deps, options{}, tt.filePath(h))
			if err == nil {
				t.Fatal("expected an error")
			}

			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func failingFetcher(bool) (core.RegistryFetcher, error) {
	return nil, errors.New("no cache dir")
}

func TestRunCheck_RendersResolvedState(t *testing.T) {
	h := newHarness(t)

	if err := runCheck(h.deps, options{}, "postgres:16.2"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := h.out.String()

	for _, want := range []string{
		"📦 Image:  postgres",
		"🏷  Current: 16.2",
		"🚀 Latest:  16.2.1 (minor)",
	} {
		assertContains(t, out, want)
	}
}

// A major candidate must be reported even though it is never offered as a
// default, or the user cannot see that the run left something on the table.
func TestRunCheck_ReportsMajorWithoutSelectingIt(t *testing.T) {
	h := newHarness(t)
	h.deps.NewFetcher = func(bool) (core.RegistryFetcher, error) {
		return stubFetcher{resolve: func(current core.ImageUpdate) core.ImageUpdate {
			updated := upgrade(current)
			updated.UpdateType = core.UpdateTypeMajor
			updated.MajorTag = "17.0.0"

			return updated
		}}, nil
	}

	if err := runCheck(h.deps, options{}, "postgres:16.2"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertContains(t, h.out.String(), "⚠️  Major available: 17.0.0 (not preselected)")
}

func TestRunCheck_UpToDate(t *testing.T) {
	h := newHarness(t)
	h.deps.NewFetcher = func(bool) (core.RegistryFetcher, error) {
		return stubFetcher{resolve: func(current core.ImageUpdate) core.ImageUpdate {
			current.NewTag = current.OldTag
			current.UpdateType = core.UpdateTypeNone

			return current
		}}, nil
	}

	if err := runCheck(h.deps, options{}, "postgres:16.2"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertContains(t, h.out.String(), "✅ Up to date!")
}

func TestRunCheck_Errors(t *testing.T) {
	tests := []struct {
		name    string
		fetcher func(bool) (core.RegistryFetcher, error)
		wantErr string
	}{
		{
			name:    "fetcher cannot be built",
			fetcher: failingFetcher,
			wantErr: "failed to initialize fetcher",
		},
		{
			name: "registry lookup fails",
			fetcher: func(bool) (core.RegistryFetcher, error) {
				return stubFetcher{err: errors.New("unauthorized")}, nil
			},
			wantErr: "failed to check postgres:16.2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.deps.NewFetcher = tt.fetcher

			err := runCheck(h.deps, options{}, "postgres:16.2")
			if err == nil {
				t.Fatal("expected an error")
			}

			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestNewRootCmd_RoutesSubcommandsAndFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "update a file",
			args: []string{"--force", "--verbose", "docker-compose.yml"},
			want: []string{"Force refresh activated", "✅ [1/2]"},
		},
		{
			name: "short flags",
			args: []string{"-f", "-v", "docker-compose.yml"},
			want: []string{"Force refresh activated", "✅ [1/2]"},
		},
		{
			name: "check an image",
			args: []string{"check", "postgres:16.2"},
			want: []string{"🔍 Checking postgres:16.2"},
		},
		{
			name: "force refresh reaches check",
			args: []string{"--force", "check", "postgres:16.2"},
			want: []string{"🔍 Checking postgres:16.2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			cmd := newRootCmd(h.deps)

			// The fixture lives in a temp dir, so the path is substituted in.
			args := make([]string, 0, len(tt.args))
			for _, arg := range tt.args {
				if arg == "docker-compose.yml" {
					arg = h.compose
				}

				args = append(args, arg)
			}

			cmd.SetArgs(args)

			if err := cmd.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for _, want := range tt.want {
				assertContains(t, h.out.String(), want)
			}
		})
	}
}

func TestNewRootCmd_RejectsBadUsage(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "no arguments", args: nil},
		{name: "too many arguments", args: []string{"a.yml", "b.yml"}},
		{name: "check without an image", args: []string{"check"}},
		{name: "unknown flag", args: []string{"--nope", "docker-compose.yml"}},
		{name: "unknown subcommand", args: []string{"upgrade"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			cmd := newRootCmd(h.deps)
			cmd.SetArgs(tt.args)

			if err := cmd.Execute(); err == nil {
				t.Fatal("expected a usage error")
			}
		})
	}
}

// main owns the only print of an error, so cobra must stay silent. Otherwise a
// failure is reported twice.
func TestNewRootCmd_LeavesErrorReportingToMain(t *testing.T) {
	h := newHarness(t)
	h.deps.NewFetcher = failingFetcher

	cmd := newRootCmd(h.deps)
	cmd.SetArgs([]string{h.compose})

	if !cmd.SilenceErrors || !cmd.SilenceUsage {
		t.Fatal("expected the command to silence cobra's own error reporting")
	}

	if err := cmd.Execute(); err == nil {
		t.Fatal("expected an error")
	}

	if h.out.Len() != 0 || h.errOut.Len() != 0 {
		t.Errorf("expected no output on failure, got out=%q err=%q", h.out.String(), h.errOut.String())
	}
}

func TestNewRootCmd_ReportsVersion(t *testing.T) {
	h := newHarness(t)
	cmd := newRootCmd(h.deps)
	cmd.SetArgs([]string{"--version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertContains(t, h.out.String(), Version)
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	return string(data)
}

func assertContains(t *testing.T, got, want string) {
	t.Helper()

	if !strings.Contains(got, want) {
		t.Errorf("expected %q in:\n%s", want, got)
	}
}

// A dry run must promise exactly what a real run would write, and say so in the
// output rather than claiming success.
func TestPrintApplyResult_DryRunDoesNotClaimSuccess(t *testing.T) {
	report := &core.Report{
		DryRun: true,
		Updates: []core.ImageUpdate{
			testResolved("db", "postgres", core.UpdateTypeMinor),
		},
	}

	var out bytes.Buffer
	printApplyResult(&out, report)

	assertContains(t, out.String(), "Would apply 1 update (dry run, nothing was written):")
	assertContains(t, out.String(), "[db] postgres: 16.2 -> 16.3 (minor)")

	if strings.Contains(out.String(), "Successfully applied") {
		t.Errorf("a dry run must not report a write:\n%s", out.String())
	}
}

func TestPrintApplyResult_ReportsPinnedDigest(t *testing.T) {
	report := &core.Report{
		Written: true,
		Updates: []core.ImageUpdate{
			testResolved("web", "nginx", core.UpdateTypeNone),
		},
	}

	var out bytes.Buffer
	printApplyResult(&out, report)

	assertContains(t, out.String(), "Successfully applied 1 update:")
	assertContains(t, out.String(), "[web] nginx: pinned to new digest")
}

func TestPrintApplyResult_EmptyReport(t *testing.T) {
	var out bytes.Buffer
	printApplyResult(&out, &core.Report{})

	assertContains(t, out.String(), "Everything is up to date")
}

// testResolved is a resolved, selected update as the report would carry it.
func testResolved(service, image string, updateType core.UpdateType) core.ImageUpdate {
	return core.ImageUpdate{
		FilePath:    "docker-compose.yml",
		LineNumber:  4,
		ServiceName: service,
		ImageName:   image,
		OldTag:      "16.2",
		NewTag:      "16.3",
		NewDigest:   "sha256:newdigest",
		UpdateType:  updateType,
		Selected:    true,
	}
}

// resolveAs resolves every image to the given update type, so a --mode test can
// put every magnitude in front of the run at once.
func resolveAs(updateType core.UpdateType) func(core.ImageUpdate) core.ImageUpdate {
	return func(current core.ImageUpdate) core.ImageUpdate {
		updated := upgrade(current)
		updated.UpdateType = updateType

		return updated
	}
}

// failingRepo makes one specific image unresolvable. It has to return an error
// rather than an unchanged update: an unchanged update is "nothing to do", not
// a failure, and the two produce different reports and exit codes.
func failingRepo(image string) func(bool) (core.RegistryFetcher, error) {
	return func(bool) (core.RegistryFetcher, error) {
		return stubFetcher{
			resolve: upgrade,
			failing: map[string]error{image: errRepo},
		}, nil
	}
}

var errRepo = errors.New("registry unavailable")

func TestRunUpdate_DryRunLeavesTheFileUntouched(t *testing.T) {
	h := newHarness(t)

	if err := runUpdate(h.deps, options{DryRun: true}, h.compose); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if written := readFile(t, h.compose); written != composeFixture {
		t.Errorf("expected the file to be untouched:\n%s", written)
	}
}

func TestRunUpdate_QuietSuppressesProgressButKeepsTheResult(t *testing.T) {
	h := newHarness(t)

	if err := runUpdate(h.deps, options{Quiet: true}, h.compose); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := h.out.String()

	// The result is the point of the run, so it survives --quiet.
	assertContains(t, out, "Successfully applied 2 updates")

	// The progress chatter does not.
	for _, unwanted := range []string{"Checked 2 images", "Hoisting sails", "🚢"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("expected %q to be suppressed, got:\n%s", unwanted, out)
		}
	}
}

func TestRunUpdate_QuietSuppressesTheSkippedBlock(t *testing.T) {
	h := newHarness(t)
	h.deps.NewFetcher = failingRepo("postgres")

	if err := runUpdate(h.deps, options{Quiet: true, Yes: true}, h.compose); err == nil {
		t.Fatal("expected an incomplete run to fail a --yes run")
	}

	if strings.Contains(h.errOut.String(), "Skipped 1 image") {
		t.Errorf("expected the skipped block to be suppressed, got:\n%s", h.errOut.String())
	}
}

// stdout must be exactly one JSON document, or the flag is useless in a pipe.
func TestRunUpdate_JSONOwnsStdoutAlone(t *testing.T) {
	h := newHarness(t)

	if err := runUpdate(h.deps, options{JSON: true}, h.compose); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report core.Report
	if err := json.Unmarshal(h.out.Bytes(), &report); err != nil {
		t.Fatalf("stdout is not a single JSON document: %v\n%s", err, h.out.String())
	}

	if report.File != h.compose {
		t.Errorf("expected the report to name the file, got %q", report.File)
	}

	if len(report.Updates) != 2 || !report.Written {
		t.Errorf("expected 2 written updates, got %+v", report)
	}

	// The prose moved to stderr rather than disappearing, so a CI log still
	// shows what was attempted.
	assertContains(t, h.errOut.String(), "Hoisting sails")
	assertContains(t, h.errOut.String(), "Checked 2 images")
}

func TestRunUpdate_JSONAndVerboseAreRejected(t *testing.T) {
	h := newHarness(t)

	err := runUpdate(h.deps, options{JSON: true, Verbose: true}, h.compose)
	if err == nil {
		t.Fatal("expected an error")
	}

	if !strings.Contains(err.Error(), "--json and --verbose") {
		t.Errorf("expected a combination error, got %v", err)
	}

	// The run must not have started.
	if h.out.Len() != 0 {
		t.Errorf("expected no output, got %q", h.out.String())
	}
}

func TestRunUpdate_YesAppliesWithoutAsking(t *testing.T) {
	h := newHarness(t)
	// A prompter that would panic if it were consulted stands in for the TUI.
	h.deps.NewPrompter = func(core.UpdateType) core.Prompter {
		t.Error("the interactive selector must not be built for a --yes run")

		return acceptAllPrompter{}
	}

	if err := runUpdate(h.deps, options{Yes: true}, h.compose); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertContains(t, h.out.String(), "Successfully applied 2 updates")
}

func TestRunUpdate_YesAppliesMinorButNotMajor(t *testing.T) {
	h := newHarness(t)
	h.deps.NewFetcher = func(bool) (core.RegistryFetcher, error) {
		return stubFetcher{resolve: resolveAs(core.UpdateTypeMajor)}, nil
	}

	if err := runUpdate(h.deps, options{Yes: true, Mode: "minor"}, h.compose); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Nothing is within a minor cap, so the file is left alone.
	if written := readFile(t, h.compose); written != composeFixture {
		t.Errorf("expected no changes, got:\n%s", written)
	}

	assertContains(t, h.out.String(), "Everything is up to date")
}

// Interactively, --mode only changes what is preselected: the row is still
// offered so the user can see it and opt in.
func TestRunUpdate_ModeCapsTheInteractiveSelector(t *testing.T) {
	h := newHarness(t)

	var capped core.UpdateType

	h.deps.NewPrompter = func(maxUpdate core.UpdateType) core.Prompter {
		capped = maxUpdate

		return acceptAllPrompter{}
	}

	if err := runUpdate(h.deps, options{Mode: "patch"}, h.compose); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capped != core.UpdateTypePatch {
		t.Errorf("expected the selector to be capped at patch, got %q", capped)
	}
}

func TestOptions_MaxUpdate(t *testing.T) {
	tests := []struct {
		mode string
		want core.UpdateType
	}{
		{mode: "", want: ""},
		{mode: "major", want: ""},
		{mode: "MINOR", want: core.UpdateTypeMinor},
		{mode: "  patch  ", want: core.UpdateTypePatch},
	}

	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			got, err := options{Mode: tt.mode}.maxUpdate()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got != tt.want {
				t.Errorf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

// The error has to name the valid modes, otherwise the fix is a guess.
func TestOptions_MaxUpdate_RejectsUnknownMode(t *testing.T) {
	for _, mode := range []string{"nonsense", "MINOR2", "none"} {
		t.Run(mode, func(t *testing.T) {
			_, err := options{Mode: mode}.maxUpdate()
			if err == nil {
				t.Fatalf("expected an error for %q", mode)
			}

			if !strings.Contains(err.Error(), "patch, minor, major") {
				t.Errorf("expected the error to list the valid modes, got %v", err)
			}
		})
	}
}

// A typo must be caught before any registry traffic.
func TestRunUpdate_InvalidModeFailsBeforeFetching(t *testing.T) {
	h := newHarness(t)
	h.deps.NewFetcher = func(bool) (core.RegistryFetcher, error) {
		t.Error("the fetcher must not be built for an invalid --mode")

		return stubFetcher{resolve: upgrade}, nil
	}

	err := runUpdate(h.deps, options{Mode: "nonsense"}, h.compose)
	if err == nil {
		t.Fatal("expected an error")
	}

	if !strings.Contains(err.Error(), "invalid --mode") {
		t.Errorf("expected a mode error, got %v", err)
	}
}

func TestRunUpdate_WithoutATerminalFails(t *testing.T) {
	h := newHarness(t)
	h.deps.IsInteractive = func() bool { return false }

	err := runUpdate(h.deps, options{}, h.compose)
	if err == nil {
		t.Fatal("expected an error")
	}

	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("expected the error to suggest --yes, got %v", err)
	}
}

// A dry run needs no selection from anyone, so it is answerable without a
// terminal and without --yes.
func TestRunUpdate_DryRunStillNeedsASelector(t *testing.T) {
	h := newHarness(t)
	h.deps.IsInteractive = func() bool { return false }

	if err := runUpdate(h.deps, options{DryRun: true}, h.compose); err == nil {
		t.Fatal("expected an error: --dry-run previews a selection, it does not make one")
	}
}

func TestExitCodeFor(t *testing.T) {
	withFailures := &core.Report{Failures: []core.Failure{{Image: "nginx", Message: "unauthorized"}}}
	clean := &core.Report{}

	tests := []struct {
		name     string
		opts     options
		report   *core.Report
		wantFail bool
	}{
		{name: "no failures", opts: options{Yes: true, JSON: true}, report: clean},
		{name: "failures are tolerated interactively", opts: options{}, report: withFailures},
		{name: "failures fail a yes run", opts: options{Yes: true}, report: withFailures, wantFail: true},
		{name: "failures fail a json run", opts: options{JSON: true}, report: withFailures, wantFail: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := exitCodeFor(tt.opts, tt.report)

			if tt.wantFail {
				if !errors.Is(err, errIncompleteRun) {
					t.Fatalf("expected errIncompleteRun, got %v", err)
				}

				return
			}

			if err != nil {
				t.Errorf("expected no error, got %v", err)
			}
		})
	}
}

// filteredFixture marks one service with the inline directive, so a run has
// something to filter.
const filteredFixture = `services:
  web:
    image: nginx:1.25.0
  db:
    image: postgres:16.2 # shiphoist-ignore
`

// newFilteredHarness writes a fixture containing the ignore directive.
func newFilteredHarness(t *testing.T) *harness {
	t.Helper()

	h := newHarness(t)
	h.compose = writeCompose(t, "docker-compose.yml", filteredFixture)

	return h
}

func writeCompose(t *testing.T, name, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	return path
}

// An ignored reference is never fetched, never patched, and is stated.
func TestRunUpdate_IgnoreDirectiveIsHonoured(t *testing.T) {
	h := newFilteredHarness(t)

	if err := runUpdate(h.deps, options{}, h.compose); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	written := readFile(t, h.compose)

	if !strings.Contains(written, "image: postgres:16.2 # shiphoist-ignore") {
		t.Errorf("expected the ignored reference to be untouched:\n%s", written)
	}

	// The other reference in the same file is still updated.
	if !strings.Contains(written, "image: nginx:1.25.0.1@sha256:newdigest") {
		t.Errorf("expected nginx to be updated:\n%s", written)
	}

	assertContains(t, h.out.String(), "Successfully applied 1 update")
	assertContains(t, h.out.String(), "Not checked (1)")
	assertContains(t, h.out.String(), "postgres (line 5): ignore-directive")
}

func TestRunUpdate_ExcludeSkipsRepositories(t *testing.T) {
	h := newHarness(t)

	if err := runUpdate(h.deps, options{Exclude: `^redis$`}, h.compose); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	written := readFile(t, h.compose)

	if !strings.Contains(written, "image: redis:7.2.1") {
		t.Errorf("expected redis to be untouched:\n%s", written)
	}

	assertContains(t, h.out.String(), "Not checked (1)")
	assertContains(t, h.out.String(), "redis (line 5): excluded")
}

// --exclude and the inline directive stack, and the report accounts for both.
func TestRunUpdate_FiltersCombine(t *testing.T) {
	h := newFilteredHarness(t)

	opts := options{Exclude: `^nginx$`, JSON: true}
	if err := runUpdate(h.deps, opts, h.compose); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var report core.Report
	if err := json.Unmarshal(h.out.Bytes(), &report); err != nil {
		t.Fatalf("stdout is not a JSON document: %v\n%s", err, h.out.String())
	}

	if len(report.Filtered) != 2 {
		t.Fatalf("expected 2 filtered references, got %+v", report.Filtered)
	}

	// Ordered by where they appear in the file, whatever removed them.
	if report.Filtered[0].Image != "nginx" || report.Filtered[1].Image != "postgres" {
		t.Errorf("expected nginx then postgres, got %+v", report.Filtered)
	}

	if report.Filtered[0].Reason != discovery.ReasonExcluded {
		t.Errorf("expected %q, got %q", discovery.ReasonExcluded, report.Filtered[0].Reason)
	}

	// Both references were declared but neither was checked, so References
	// must not claim to cover the file on its own.
	if report.References != 0 {
		t.Errorf("expected 0 checked references, got %d", report.References)
	}
}

func TestRunUpdate_ExcludeRejectsABadPattern(t *testing.T) {
	h := newHarness(t)
	h.deps.NewFetcher = func(bool) (core.RegistryFetcher, error) {
		t.Error("the fetcher must not be built for an invalid --exclude")

		return stubFetcher{resolve: upgrade}, nil
	}

	err := runUpdate(h.deps, options{Exclude: "([unclosed"}, h.compose)
	if err == nil {
		t.Fatal("expected an error")
	}

	if !strings.Contains(err.Error(), "invalid --exclude") {
		t.Errorf("expected an exclude error, got %v", err)
	}
}

func TestRunUpdate_QuietSuppressesTheFilteredBlock(t *testing.T) {
	h := newFilteredHarness(t)

	if err := runUpdate(h.deps, options{Quiet: true}, h.compose); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(h.out.String(), "Not checked") {
		t.Errorf("expected the filtered block to be suppressed, got:\n%s", h.out.String())
	}
}

// The flags that only mean something for a file must not be silently accepted
// by `check`.
func TestNewRootCmd_CheckRejectsUpdateOnlyFlags(t *testing.T) {
	for _, flag := range []string{"--json", "--dry-run", "--yes", "--mode", "--exclude", "--quiet"} {
		t.Run(flag, func(t *testing.T) {
			h := newHarness(t)
			cmd := newRootCmd(h.deps)
			cmd.SetArgs([]string{"check", "postgres:16.2", flag})

			if err := cmd.Execute(); err == nil {
				t.Errorf("expected %s to be rejected by check", flag)
			}
		})
	}
}

func TestNewRootCmd_CheckKeepsSharedFlags(t *testing.T) {
	h := newHarness(t)
	cmd := newRootCmd(h.deps)
	cmd.SetArgs([]string{"--verbose", "check", "postgres:16.2"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertContains(t, h.out.String(), "🔍 Checking postgres:16.2")
}

func TestNewRootCmd_RoutesExclude(t *testing.T) {
	h := newHarness(t)
	cmd := newRootCmd(h.deps)
	cmd.SetArgs([]string{"--exclude", `^redis$`, h.compose})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertContains(t, h.out.String(), "redis (line 5): excluded")
}

// dockerfileFixture exercises the shapes a Dockerfile has that a Compose file
// does not: a platform flag, a build argument, `scratch`, and a `COPY --from`
// that must never be read as a base image.
const dockerfileFixture = `ARG NODE_VERSION=22
FROM --platform=linux/amd64 node:$NODE_VERSION AS builder
WORKDIR /app
FROM node:22-alpine AS deps # keep in sync
RUN npm ci
FROM scratch
COPY --from=deps /out /out
`

// newDockerfileHarness writes a Dockerfile fixture.
func newDockerfileHarness(t *testing.T) *harness {
	t.Helper()

	h := newHarness(t)
	h.compose = writeCompose(t, "Dockerfile", dockerfileFixture)

	return h
}

func TestRunUpdate_DiscoversDockerfileStages(t *testing.T) {
	h := newDockerfileHarness(t)

	if err := runUpdate(h.deps, options{DryRun: true, Yes: true}, h.compose); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := h.out.String()

	// The stage alias is the identifier, so the user can tell stages apart.
	assertContains(t, out, "[deps] node")

	// The variable reference and `scratch` are reported rather than dropped.
	assertContains(t, out, "Would apply 1 update")
	assertContains(t, out, "Not checked (2)")
	assertContains(t, out, "node:$NODE_VERSION")
	assertContains(t, out, "scratch")
}

func TestRunUpdate_PatchesDockerfile(t *testing.T) {
	h := newDockerfileHarness(t)

	if err := runUpdate(h.deps, options{Yes: true}, h.compose); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	written := readFile(t, h.compose)

	// The instruction, the alias and the trailing comment all survive. The
	// harness stub resolves 22-alpine to 22-alpine.1.
	if !strings.Contains(written, "FROM node:22-alpine.1@sha256:newdigest AS deps # keep in sync") {
		t.Errorf("expected the FROM line to be rewritten in place:\n%s", written)
	}

	// The build-argument stage and scratch are untouched.
	if !strings.Contains(written, "FROM --platform=linux/amd64 node:$NODE_VERSION AS builder") {
		t.Errorf("expected the variable stage to be untouched:\n%s", written)
	}

	if !strings.Contains(written, "FROM scratch") {
		t.Errorf("expected scratch to be untouched:\n%s", written)
	}

	// A COPY --from must never be rewritten into a base image.
	if strings.Contains(written, "FROM deps") {
		t.Errorf("a COPY --from was treated as a base image:\n%s", written)
	}
}

// --exclude works on Dockerfiles too, since it only needs the repository.
func TestRunUpdate_ExcludeAppliesToDockerfiles(t *testing.T) {
	h := newDockerfileHarness(t)

	opts := options{DryRun: true, Yes: true, Exclude: `^node$`}
	if err := runUpdate(h.deps, opts, h.compose); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Nothing is left to apply, so the run reports nothing to do rather than an
	// empty list of updates.
	assertContains(t, h.out.String(), "Everything is up to date")

	// All three skipped references are accounted for, whatever removed them.
	assertContains(t, h.out.String(), "Not checked (3)")
	assertContains(t, h.out.String(), "variable-reference")
	assertContains(t, h.out.String(), "reserved-base")
	assertContains(t, h.out.String(), "excluded")
}

// A Compose file keeps working unchanged: the format is chosen per file.
func TestRunUpdate_ComposeStillDetected(t *testing.T) {
	h := newHarness(t)

	if err := runUpdate(h.deps, options{DryRun: true, Yes: true}, h.compose); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertContains(t, h.out.String(), "[db] postgres")
	assertContains(t, h.out.String(), "[cache] redis")
}
