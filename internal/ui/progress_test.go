package ui

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestProgress_SilentUntilStop(t *testing.T) {
	out := &bytes.Buffer{}
	progress := NewProgress(out, 3, ProgressOptions{Width: 80})

	progress.Advance("nginx")
	progress.Advance("postgres")

	if out.Len() != 0 {
		t.Errorf("expected nothing before Stop, got %q", out.String())
	}

	progress.Stop("done")

	if !strings.Contains(out.String(), "done") {
		t.Errorf("expected the summary at Stop, got %q", out.String())
	}
}

// A non-TTY sink must never receive control characters, or CI logs fill with
// escape sequences.
func TestProgress_EmitsNoControlCharactersWhenNotInteractive(t *testing.T) {
	out := &bytes.Buffer{}
	progress := NewProgress(out, 5, ProgressOptions{Width: 80})

	for range 5 {
		progress.Advance("nginx")
	}

	progress.Stop("summary")

	got := out.String()
	if strings.Contains(got, "\x1b") || strings.Contains(got, "\r") {
		t.Errorf("expected no escape sequences, got %q", got)
	}
}

func TestProgress_BarRendersInPlace(t *testing.T) {
	out := &bytes.Buffer{}
	progress := NewProgress(out, 4, ProgressOptions{Interactive: true, Width: 80})

	progress.Advance("nginx")
	progress.Advance("postgres")
	progress.Stop("summary")

	got := out.String()

	if !strings.Contains(got, clearLine) {
		t.Errorf("expected the line to be erased before each redraw, got %q", got)
	}

	if !strings.Contains(got, barFilled) || !strings.Contains(got, barEmpty) {
		t.Errorf("expected a filled and empty bar, got %q", got)
	}

	if !strings.Contains(got, "2/4") {
		t.Errorf("expected the counter, got %q", got)
	}

	if !strings.Contains(got, "postgres") {
		t.Errorf("expected the current image, got %q", got)
	}

	// The summary must land on a clean line, not after a partial bar.
	summaryIndex := strings.Index(got, "summary")
	if strings.Contains(got[summaryIndex-len(clearLine):summaryIndex], barFilled) {
		t.Errorf("expected the bar to be erased before the summary, got %q", got)
	}
}

func TestProgress_BarStaysWithinTerminalWidth(t *testing.T) {
	for _, width := range []int{200, 120, 80, 60, 45, 40, 30, 20} {
		out := &bytes.Buffer{}
		progress := NewProgress(out, 7, ProgressOptions{Interactive: true, Width: width})

		progress.Advance("ghcr.io/potibm/billedapparat")
		progress.Stop("summary")

		for _, line := range visibleLines(out.String()) {
			if got := ansi.StringWidth(line); got > width {
				t.Errorf("width %d: line is %d columns: %q", width, got, line)
			}
		}
	}
}

// Too narrow for a bar: the counter and label must still be useful.
func TestProgress_NarrowTerminalDropsTheBar(t *testing.T) {
	out := &bytes.Buffer{}
	progress := NewProgress(out, 7, ProgressOptions{Interactive: true, Width: 20})

	progress.Advance("nginx")
	progress.Stop("summary")

	line := firstLine(out.String())

	if strings.Contains(line, barFilled) {
		t.Errorf("expected no bar on a narrow terminal, got %q", line)
	}

	if !strings.Contains(line, "1/7") {
		t.Errorf("expected the counter to remain, got %q", line)
	}
}

func TestProgress_VerboseWritesOneLinePerImage(t *testing.T) {
	out := &bytes.Buffer{}
	progress := NewProgress(out, 3, ProgressOptions{Interactive: true, Verbose: true, Width: 80})

	progress.Advance("nginx")
	progress.Advance("postgres")
	progress.Stop("summary")

	got := out.String()

	if strings.Contains(got, clearLine) {
		t.Errorf("expected no in-place redraw in verbose mode, got %q", got)
	}

	if strings.Count(got, "\n") != 3 {
		t.Errorf("expected 2 progress lines and 1 summary, got:\n%q", got)
	}

	for _, want := range []string{"[1/3]", "[2/3]", "nginx", "postgres"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in verbose output, got:\n%s", want, got)
		}
	}
}

func TestProgress_VerboseReportsFailuresInline(t *testing.T) {
	out := &bytes.Buffer{}
	progress := NewProgress(out, 2, ProgressOptions{Verbose: true, Width: 80})

	progress.Fail("nginx", errTest)
	progress.Stop("summary")

	got := out.String()

	if !strings.Contains(got, "⚠️") || !strings.Contains(got, "boom") {
		t.Errorf("expected the failure inline, got:\n%s", got)
	}
}

// In bar mode a failure must not print, or the single line would break.
func TestProgress_FailuresAreDeferredInBarMode(t *testing.T) {
	out := &bytes.Buffer{}
	progress := NewProgress(out, 2, ProgressOptions{Interactive: true, Width: 80})

	progress.Fail("nginx", errTest)
	progress.Fail("postgres", errTest)
	progress.Stop("summary")

	got := out.String()

	if !strings.Contains(got, "Skipped 2 images") {
		t.Errorf("expected a skipped block, got:\n%s", got)
	}

	if !strings.Contains(got, "nginx") || !strings.Contains(got, "postgres") {
		t.Errorf("expected both failures listed, got:\n%s", got)
	}

	// The block must come after the summary, not before it.
	if strings.Index(got, "Skipped") < strings.Index(got, "summary") {
		t.Errorf("expected failures after the summary, got:\n%s", got)
	}
}

func TestProgress_FailureCountIsSingular(t *testing.T) {
	out := &bytes.Buffer{}
	progress := NewProgress(out, 2, ProgressOptions{Width: 80})

	progress.Fail("nginx", errTest)
	progress.Stop("summary")

	if !strings.Contains(out.String(), "Skipped 1 image:") {
		t.Errorf("expected singular wording, got:\n%s", out.String())
	}
}

func TestProgress_StopReturnsFailures(t *testing.T) {
	out := &bytes.Buffer{}
	progress := NewProgress(out, 3, ProgressOptions{Width: 80})

	progress.Advance("nginx")
	progress.Fail("postgres", errTest)

	if len(progress.Failures()) != 1 {
		t.Errorf("expected 1 recorded failure, got %d", len(progress.Failures()))
	}

	failures := progress.Stop("summary")
	if len(failures) != 1 {
		t.Fatalf("expected Stop to return 1 failure, got %d", len(failures))
	}

	if failures[0].Label != "postgres" {
		t.Errorf("expected the label to be preserved, got %q", failures[0].Label)
	}

	// Stop must be safe to call twice without duplicating the block.
	out.Reset()

	progress.Stop("summary again")

	if strings.Contains(out.String(), "Skipped") {
		t.Errorf("expected failures to be cleared after Stop, got:\n%s", out.String())
	}
}

func TestProgress_Done(t *testing.T) {
	out := &bytes.Buffer{}
	progress := NewProgress(out, 3, ProgressOptions{Width: 80})

	progress.Advance("a")
	progress.Fail("b", errTest)

	if got := progress.Done(); got != 2 {
		t.Errorf("Done() = %d, want 2", got)
	}
}

func TestProgress_ZeroTotalDoesNotPanic(t *testing.T) {
	out := &bytes.Buffer{}
	progress := NewProgress(out, 0, ProgressOptions{Interactive: true, Width: 80})

	// The guard exists so an empty run cannot divide by zero. The counter
	// clamps to the total, so an empty run reads as 0/0.
	progress.Advance("nginx")
	progress.Stop("summary")

	if !strings.Contains(out.String(), "0/0") {
		t.Errorf("expected a 0/0 counter, got:\n%s", out.String())
	}

	if !strings.Contains(out.String(), barEmpty) {
		t.Errorf("expected an empty bar, got:\n%s", out.String())
	}
}

func TestProgress_ShortLabelIsNotOverPadded(t *testing.T) {
	out := &bytes.Buffer{}
	progress := NewProgress(out, 4, ProgressOptions{Interactive: true, Width: 80})

	progress.Advance("a")
	progress.Stop("summary")

	line := strings.TrimPrefix(out.String(), clearLine)
	if strings.Contains(line, "a  ") {
		t.Errorf("expected a short label to be left as-is, got %q", line)
	}
}

func TestProgressModeFor(t *testing.T) {
	tests := []struct {
		name     string
		opts     ProgressOptions
		expected progressMode
	}{
		{name: "silent by default", opts: ProgressOptions{}, expected: modeSilent},
		{name: "interactive", opts: ProgressOptions{Interactive: true}, expected: modeBar},
		{name: "verbose wins", opts: ProgressOptions{Interactive: true, Verbose: true}, expected: modeVerbose},
		{name: "verbose without a terminal", opts: ProgressOptions{Verbose: true}, expected: modeVerbose},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := progressModeFor(tc.opts); got != tc.expected {
				t.Errorf("progressModeFor() = %v, want %v", got, tc.expected)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		expected string
	}{
		{name: "sub second", duration: 820 * time.Millisecond, expected: "820ms"},
		{name: "seconds", duration: 4200 * time.Millisecond, expected: "4.2s"},
		{name: "minutes", duration: 90 * time.Second, expected: "1m30s"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatDuration(tc.duration); got != tc.expected {
				t.Errorf("FormatDuration(%v) = %q, want %q", tc.duration, got, tc.expected)
			}
		})
	}
}

func TestClip(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		width    int
		expected string
	}{
		{name: "fits", value: "abc", width: 5, expected: "abc"},
		{name: "truncates", value: "abcdef", width: 4, expected: "abc" + ellipsis},
		{name: "zero width", value: "abc", width: 0, expected: ""},
		{name: "negative width", value: "abc", width: -5, expected: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := clip(tc.value, tc.width); got != tc.expected {
				t.Errorf("clip(%q, %d) = %q, want %q", tc.value, tc.width, got, tc.expected)
			}
		})
	}
}

// A run advances from one goroutine per image. Run with -race.
func TestProgress_ConcurrentAdvanceIsRaceFree(t *testing.T) {
	out := &bytes.Buffer{}
	progress := NewProgress(out, 64, ProgressOptions{Verbose: true, Width: 80})

	var wg sync.WaitGroup

	for range 64 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			progress.Advance("img")
		}()
	}

	wg.Wait()

	progress.Stop("summary")

	if got := progress.Done(); got != 64 {
		t.Errorf("expected 64 recorded advances, got %d", got)
	}

	if lines := strings.Count(out.String(), "["); lines != 64 {
		t.Errorf("expected 64 progress lines, got %d", lines)
	}
}

// firstLine returns the first visible line of rendered output.
func firstLine(s string) string {
	lines := visibleLines(s)
	if len(lines) == 0 {
		return ""
	}

	return lines[0]
}

// visibleLines splits rendered output into the lines a user would actually see,
// discarding the in-place redraw separators.
func visibleLines(s string) []string {
	var lines []string

	for _, part := range strings.Split(s, clearLine) {
		for _, line := range strings.Split(part, "\n") {
			if line != "" {
				lines = append(lines, line)
			}
		}
	}

	return lines
}

var errTest = errStub("boom")

type errStub string

func (e errStub) Error() string { return string(e) }

// A counting mistake must not display a ratio above 1, which would look
// wrong to the user.
func TestProgress_CounterIsClampedToTotal(t *testing.T) {
	out := &bytes.Buffer{}
	progress := NewProgress(out, 2, ProgressOptions{Interactive: true, Width: 80})

	progress.Advance("a")
	progress.Advance("b")
	progress.Advance("c")
	progress.Stop("summary")

	if strings.Contains(out.String(), "3/2") {
		t.Errorf("expected the counter to be clamped, got:\n%s", out.String())
	}
}
