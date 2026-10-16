package main

import (
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"sync"

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

	// Files are the paths named by --file, combined with the positional
	// arguments. Either way works because a shell expands a glob before
	// shiphoist sees it, which makes `--file '**/docker-compose.yml'` the way
	// to pass a recursive pattern a shell would not grow for you.
	Files []string

	// Suffixes are extra file suffixes that mark a Compose file, from
	// --suffix. A file named docker-compose.yml.j2 is an Ansible template for
	// a Compose file, and the reference in it is updated exactly like the
	// rendered result would be.
	Suffixes []string
}

// buildDiscoverer assembles the discovery chain for the run.
//
// The format follows from each file name, so `shiphoist Dockerfile` needs no
// extra flag and a run over several files in several formats works with the same
// command. The regex is compiled once here rather than per file, so a bad
// pattern costs no registry traffic. An empty --exclude installs no decorator at
// all, which keeps the common path free of an extra layer.
//
// The returned set also answers what was filtered, which the engine cannot: it
// moves on once discovery is done, and only a discoverer still knows which
// references a directive removed.
func buildDiscoverer(opts options) (*discovererSet, error) {
	set := &discovererSet{}

	if strings.TrimSpace(opts.Exclude) == "" {
		return set, nil
	}

	pattern, err := regexp.Compile(opts.Exclude)
	if err != nil {
		return nil, fmt.Errorf("invalid --exclude %q: %w", opts.Exclude, err)
	}

	set.decorate = func(filePath string) core.Discoverer {
		return &discovery.ExcludeDiscoverer{Inner: discovery.ForFile(filePath), Pattern: pattern}
	}

	return set, nil
}

// discovererSet builds the discoverer for one file and remembers what each of
// them removed.
//
// The engine asks for a discoverer per file, so the set hands out a fresh one
// each time and keeps it. Reading Filtered afterwards therefore reports what
// the run actually skipped, rather than what a second, unused discoverer would
// have skipped.
type discovererSet struct {
	// decorate wraps the plain discoverer for a file. When nil, no filter is
	// installed and discovery.ForFile is used as is.
	decorate func(filePath string) core.Discoverer

	mu      sync.Mutex
	created []core.FilteredDiscoverer
}

// Filtered returns every reference any discoverer in the run removed, ordered
// by file and then by line, so a multi-file report reads in the order the files
// were processed.
func (s *discovererSet) Filtered() []core.Filtered {
	s.mu.Lock()
	defer s.mu.Unlock()

	var filtered []core.Filtered

	for _, discoverer := range s.created {
		filtered = append(filtered, discoverer.Filtered()...)
	}

	slices.SortStableFunc(filtered, func(a, b core.Filtered) int {
		if a.FilePath != b.FilePath {
			return strings.Compare(a.FilePath, b.FilePath)
		}

		return a.LineNumber - b.LineNumber
	})

	return filtered
}

// discovererFor returns the discoverer for filePath.
func (s *discovererSet) discovererFor(filePath string) core.Discoverer {
	var discoverer core.Discoverer

	if s.decorate != nil {
		discoverer = s.decorate(filePath)
	} else {
		discoverer = discovery.ForFile(filePath)
	}

	if filtered, ok := discoverer.(core.FilteredDiscoverer); ok {
		s.mu.Lock()
		s.created = append(s.created, filtered)
		s.mu.Unlock()
	}

	return discoverer
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
