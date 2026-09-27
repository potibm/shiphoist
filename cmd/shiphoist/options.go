package main

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/potibm/shiphoist/internal/core"
	"github.com/potibm/shiphoist/internal/discovery"
	"github.com/potibm/shiphoist/internal/prompting"
)

// options are the resolved flags for one invocation.
//
// They travel as a struct rather than a growing list of booleans: there are
// enough flags now that positional parameters would be unreadable, and each new
// one would widen every call site.
type options struct {
	// ForceRefresh bypasses the on-disk registry cache.
	ForceRefresh bool

	// Verbose reports one line per image instead of a single progress bar.
	Verbose bool

	// DryRun reports the updates that would apply without writing them.
	DryRun bool

	// Yes skips the interactive selection and applies everything within Mode.
	Yes bool

	// JSON writes a machine-readable report to stdout.
	JSON bool

	// Quiet suppresses the progress bar, the banner, the summary and the
	// skipped-images block. The result itself is still reported, because that
	// is the point of the run.
	Quiet bool

	// Mode is the raw --mode value, validated by maxUpdate.
	Mode string

	// Exclude is a regular expression matched against image repositories.
	// Filtering happens at discovery, so it is resolved by buildDiscoverer.
	Exclude string
}

// buildDiscoverer assembles the discovery chain for a file.
//
// The format follows from the file name, so `shiphoist Dockerfile` needs no
// extra flag. The regex is compiled here rather than where it is used, so a bad
// pattern costs no registry traffic. An empty --exclude installs no decorator at
// all, which keeps the common path free of an extra layer.
func buildDiscoverer(opts options, filePath string) (core.Discoverer, error) {
	discoverer := discovery.ForFile(filePath)

	if strings.TrimSpace(opts.Exclude) == "" {
		return discoverer, nil
	}

	pattern, err := regexp.Compile(opts.Exclude)
	if err != nil {
		return nil, fmt.Errorf("invalid --exclude %q: %w", opts.Exclude, err)
	}

	return &discovery.ExcludeDiscoverer{Inner: discoverer, Pattern: pattern}, nil
}

// filteredFrom returns the references a discoverer removed, or nil for one that
// does not filter.
//
// The discoverer is the only place that can honour an inline directive, since
// the comment is gone once the file has been reduced to tokens. The CLI
// assembled the chain, so it is the CLI that completes the report with it; the
// engine stays unaware of filtering.
func filteredFrom(discoverer core.Discoverer) []core.Filtered {
	filtered, ok := discoverer.(core.FilteredDiscoverer)
	if !ok {
		return nil
	}

	return filtered.Filtered()
}

// modeNames are the accepted --mode values, used for both the help text and the
// error a bad value produces.
var modeNames = []string{"patch", "minor", "major"}

// maxUpdate resolves --mode to the highest severity that may be applied.
//
// An empty or "major" value means no cap, which is the default and reproduces
// the rule that a major is never applied without being asked for. The value is
// validated before any registry work happens, so a typo costs no round-trip.
func (o options) maxUpdate() (core.UpdateType, error) {
	switch strings.ToLower(strings.TrimSpace(o.Mode)) {
	case "", "major":
		return "", nil
	case "minor":
		return core.UpdateTypeMinor, nil
	case "patch":
		return core.UpdateTypePatch, nil
	default:
		return "", fmt.Errorf("invalid --mode %q: expected one of %s", o.Mode, strings.Join(modeNames, ", "))
	}
}

// humanOut is where progress and prose go.
//
// In JSON mode stdout belongs to the report alone, so the prose moves to stderr
// and the document stays pipeable.
func humanOut(d deps, opts options) io.Writer {
	if opts.JSON {
		return d.ErrOut
	}

	return d.Out
}

// buildPrompter returns the selector for this run.
//
// The two modes differ in how the cap is enforced, which is why they are
// different types rather than one configurable selector: interactively an
// update above the cap is shown and left unchecked, while non-interactively
// there is nobody to check it.
func buildPrompter(d deps, opts options, maxUpdate core.UpdateType) core.Prompter {
	if opts.Yes {
		return prompting.Apply{MaxUpdate: maxUpdate}
	}

	return d.NewPrompter(maxUpdate)
}

// errIncompleteRun reports that the run finished but left images unresolved.
//
// It is not a failure of shiphoist and is never printed: the report already
// names the images and the reason. In an interactive run it is ignored, because
// one unreachable registry is normal. Under --yes or --json it is returned so
// the process exits non-zero, which is what makes those modes usable as a CI
// gate.
var errIncompleteRun = fmt.Errorf("some images could not be checked")

// exitCodeFor reports the process outcome of a completed run.
func exitCodeFor(opts options, report *core.Report) error {
	if len(report.Failures) == 0 {
		return nil
	}

	if opts.Yes || opts.JSON {
		return errIncompleteRun
	}

	return nil
}

// assertFlagCombination rejects flag pairs that contradict each other, before
// any work is done.
func assertFlagCombination(opts options) error {
	if opts.JSON && opts.Verbose {
		return fmt.Errorf("--json and --verbose cannot be combined: --json already reports one line per image")
	}

	return nil
}
