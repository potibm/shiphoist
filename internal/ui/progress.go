package ui

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"
)

const (
	barFilled = "█"
	barEmpty  = "░"

	// clearLine returns to the start of the line and erases it, so a shorter
	// label cannot leave the tail of a previous one behind.
	clearLine = "\r\x1b[2K"

	// minBarWidth is the narrowest bar still worth drawing.
	minBarWidth = 10

	// minLabelWidth is the space always reserved for the current image name.
	minLabelWidth = 12

	// minWidthForBar is the terminal width below which the bar is dropped
	// entirely in favour of a plain counter.
	minWidthForBar = 40

	// bracketWidth accounts for the "[" and "]" around the bar.
	bracketWidth = 2

	// The precisions below keep the summary readable without losing detail on
	// very fast runs.
	millisecondPrecision = 10 * time.Millisecond
	tenthSecondPrecision = 100 * time.Millisecond
	secondPrecision      = time.Second
)

// progressMode selects how progress reaches the user.
type progressMode int

const (
	// modeSilent writes nothing until Stop, keeping piped output and CI logs
	// free of control characters.
	modeSilent progressMode = iota
	modeBar
	modeVerbose
)

// Failure records a registry error against the image that caused it.
type Failure struct {
	Label string
	Err   error
}

// ProgressOptions configures progress reporting.
type ProgressOptions struct {
	// Interactive enables the single updating progress line. When false,
	// nothing is written until Stop.
	Interactive bool

	// Verbose writes one line per image instead of a single updating line.
	// It takes precedence over Interactive.
	Verbose bool

	// Width is the terminal width in columns.
	Width int
}

// Progress reports how far a run has got as one updating line, and collects
// failures so they can be reported once the line is gone.
//
// It is safe for concurrent use: a run advances from one goroutine per image.
type Progress struct {
	writer io.Writer
	mode   progressMode
	width  int
	total  int

	mu       sync.Mutex
	done     int
	label    string
	failures []Failure
}

// NewProgress returns a Progress for total units of work.
func NewProgress(w io.Writer, total int, opts ProgressOptions) *Progress {
	return &Progress{
		writer: w,
		mode:   progressModeFor(opts),
		width:  opts.Width,
		total:  total,
	}
}

func progressModeFor(opts ProgressOptions) progressMode {
	switch {
	case opts.Verbose:
		return modeVerbose
	case opts.Interactive:
		return modeBar
	default:
		return modeSilent
	}
}

// Advance records one completed unit of work and redraws the progress line.
func (p *Progress) Advance(label string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.done++
	p.label = label
	p.draw()
}

// Fail records a failure. Verbose output reports it straight away; otherwise it
// is deferred to Stop so that it cannot break the single updating line.
func (p *Progress) Fail(label string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.done++
	p.label = label
	p.failures = append(p.failures, Failure{Label: label, Err: err})

	if p.mode == modeVerbose {
		fmt.Fprintf(p.writer, "⚠️  [%s] %s: %v\n", p.counter(), label, err)

		return
	}

	p.draw()
}

// Stop erases the progress line, prints the summary, then prints any collected
// failures. It returns those failures so the caller can fold them into an exit
// code or a machine-readable report.
func (p *Progress) Stop(summary string) []Failure {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.mode == modeBar {
		fmt.Fprint(p.writer, clearLine)
	}

	if summary != "" {
		fmt.Fprintln(p.writer, summary)
	}

	p.printFailures()

	failures := p.failures
	p.failures = nil

	return failures
}

// Failures returns the failures collected so far.
func (p *Progress) Failures() []Failure {
	p.mu.Lock()
	defer p.mu.Unlock()

	return append([]Failure(nil), p.failures...)
}

// Done returns how many units of work have been reported.
func (p *Progress) Done() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.done
}

// draw renders the current state according to the configured mode.
func (p *Progress) draw() {
	switch p.mode {
	case modeVerbose:
		fmt.Fprintf(p.writer, "✅ [%s] %s\n", p.counter(), p.label)
	case modeBar:
		fmt.Fprint(p.writer, clearLine, p.barLine())
	case modeSilent:
	}
}

// reported is how many units of work are counted for display. It is clamped to
// the total so a counting mistake shows up as a stalled bar rather than a
// nonsensical ratio such as 19/18.
func (p *Progress) reported() int {
	return min(p.done, p.total)
}

// counter renders the progress fraction.
func (p *Progress) counter() string {
	return fmt.Sprintf("%d/%d", p.reported(), p.total)
}

// barLine renders the single updating line. The layout is
// "[████░░░░] 12/18  ghcr.io/potibm/funkapparat", with the bar dropped when the
// terminal is too narrow to carry it.
func (p *Progress) barLine() string {
	counter := p.counter()
	counterWidth := ansi.StringWidth(counter)
	chrome := bracketWidth + 2*columnGap + counterWidth

	barWidth := p.width - chrome - minLabelWidth
	if barWidth < minBarWidth {
		// Too narrow for a bar: fall back to a plain counter and label.
		return counter + strings.Repeat(" ", columnGap) + clip(p.label, p.width-counterWidth-columnGap)
	}

	label := clip(p.label, p.width-chrome-barWidth)

	return fmt.Sprintf("[%s]%s%s%s%s",
		p.bar(barWidth),
		strings.Repeat(" ", columnGap), counter,
		strings.Repeat(" ", columnGap), label,
	)
}

// bar renders the filled and empty portion of the progress bar. The total is
// guarded against zero so an empty run cannot divide by zero.
func (p *Progress) bar(width int) string {
	filled := 0
	if p.total > 0 {
		filled = min(p.reported()*width/p.total, width)
	}

	return strings.Repeat(barFilled, filled) + strings.Repeat(barEmpty, width-filled)
}

// clip shortens a value to width display columns, never returning a negative
// or zero width result.
func clip(value string, width int) string {
	if width < 1 {
		return ""
	}

	return ansi.Truncate(value, width, ellipsis)
}

// printFailures reports every collected failure as a block, once the progress
// line is out of the way.
func (p *Progress) printFailures() {
	if len(p.failures) == 0 {
		return
	}

	noun := "images"
	if len(p.failures) == 1 {
		noun = "image"
	}

	fmt.Fprintf(p.writer, "\n⚠️  Skipped %d %s:\n", len(p.failures), noun)

	for _, failure := range p.failures {
		fmt.Fprintf(p.writer, "   • %s: %v\n", failure.Label, failure.Err)
	}
}

// FormatDuration renders an elapsed time compactly for a summary line.
func FormatDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return d.Round(millisecondPrecision).String()
	case d < time.Minute:
		return d.Round(tenthSecondPrecision).String()
	default:
		return d.Round(secondPrecision).String()
	}
}
