package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/potibm/shiphoist/internal/core"
)

const composeFixture = `services:
  db:
    image: postgres:16.2
  cache:
    image: redis:7.2.1
`

// stubFetcher resolves every reference with resolve, or fails with err.
type stubFetcher struct {
	resolve func(core.ImageUpdate) core.ImageUpdate
	err     error
}

func (s stubFetcher) FetchUpdate(_ context.Context, current core.ImageUpdate) (core.ImageUpdate, error) {
	if s.err != nil {
		return current, s.err
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
			NewPrompter: func() core.Prompter { return acceptAllPrompter{} },
		},
		out:     &out,
		errOut:  &errOut,
		compose: path,
	}
}

func TestRunUpdate_PatchesFileAndSummarises(t *testing.T) {
	h := newHarness(t)

	if err := runUpdate(h.deps, h.compose, false, false); err != nil {
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

	if err := runUpdate(h.deps, h.compose, false, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertContains(t, h.out.String(), "Checked 2 images")
}

func TestRunUpdate_NothingSelectedLeavesFileUntouched(t *testing.T) {
	h := newHarness(t)
	h.deps.NewPrompter = func() core.Prompter { return rejectAllPrompter{} }

	if err := runUpdate(h.deps, h.compose, false, false); err != nil {
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
	h.deps.NewPrompter = func() core.Prompter { return nil }

	if err := runUpdate(h.deps, h.compose, false, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertContains(t, h.out.String(), "Successfully applied 2 updates")
}

func TestRunUpdate_ForceRefreshIsAnnounced(t *testing.T) {
	h := newHarness(t)

	if err := runUpdate(h.deps, h.compose, true, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertContains(t, h.out.String(), "Force refresh activated")
}

func TestRunUpdate_VerboseReachesThePipeline(t *testing.T) {
	h := newHarness(t)

	if err := runUpdate(h.deps, h.compose, false, true); err != nil {
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
				h.deps.NewPrompter = func() core.Prompter {
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

			err := runUpdate(h.deps, tt.filePath(h), false, false)
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

	if err := runCheck(h.deps, "postgres:16.2", false); err != nil {
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

	if err := runCheck(h.deps, "postgres:16.2", false); err != nil {
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

	if err := runCheck(h.deps, "postgres:16.2", false); err != nil {
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

			err := runCheck(h.deps, "postgres:16.2", false)
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
