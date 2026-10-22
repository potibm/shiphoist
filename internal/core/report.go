package core

import "encoding/json"

// Report is the outcome of one run over one or more files.
//
// It exists so a caller can describe a run without parsing human-facing output:
// the CLI renders it, a future --json mode serialises it verbatim, and a CI job
// can branch on Written or exit non-zero on Failures.
type Report struct {
	// Files are the files that were processed, in the order they were
	// resolved from the command line.
	Files []string `json:"files"`

	// Updates are the changes that were applied, in declaration order.
	Updates []ImageUpdate `json:"updates"`

	// Failures are the references that could not be resolved. A failure is
	// reported and skipped; it never aborts the run.
	Failures []Failure `json:"failures"`

	// Checked is the number of distinct registry lookups made, which is lower
	// than References whenever references were deduplicated.
	Checked int `json:"checked"`

	// References is the number of declarations discovered in the file, after
	// filtering. The file's true image count is len(References) plus
	// len(Filtered).
	References int `json:"references"`

	// Filtered are the references removed before the registry was consulted,
	// so a run never silently under-reports what it looked at.
	Filtered []Filtered `json:"filtered"`

	// ElapsedMS is the wall time spent on the registry phase, in milliseconds.
	ElapsedMS int64 `json:"elapsed_ms"`

	// Written reports whether the file was modified.
	Written bool `json:"written"`

	// DryRun reports that the run was a preview, so Written is false by
	// design rather than because nothing was selected.
	DryRun bool `json:"dry_run"`
}

// Failure records one reference that could not be resolved.
type Failure struct {
	// Image is the reference that failed, as it appeared in the file.
	Image string `json:"image"`

	// Message is the underlying reason.
	Message string `json:"message"`
}

// Filtered records one reference removed before the registry was consulted,
// either by an inline directive in the file or by a command-line filter.
//
// The file path and line number are carried so a multi-file run can say where
// each skip is, and so a report can point at what to edit.
type Filtered struct {
	// FilePath is where the reference is declared. Two files can both pin an
	// image at line 12, so the path is what tells them apart.
	FilePath string `json:"file_path"`

	// Image is the repository that was skipped. A reference whose repository
	// only exists after rendering is reported as it appears in the file.
	Image string `json:"image"`

	// LineNumber is where the reference is declared.
	LineNumber int `json:"line_number"`

	// Reason is why it was skipped, from the reason constants in the
	// discovery package.
	Reason string `json:"reason"`
}

// reportAlias breaks the recursion into MarshalJSON.
type reportAlias Report

// MarshalJSON guarantees the slices serialise as arrays, never null, however
// the report was built.
//
// A nil slice would otherwise reach a consumer as `null`, so every machine
// reader would have to handle both shapes. Enforcing it here rather than in a
// constructor means the guarantee travels with the type.
func (r Report) MarshalJSON() ([]byte, error) {
	normalised := reportAlias(r)

	if normalised.Files == nil {
		normalised.Files = []string{}
	}

	if normalised.Updates == nil {
		normalised.Updates = []ImageUpdate{}
	}

	if normalised.Failures == nil {
		normalised.Failures = []Failure{}
	}

	if normalised.Filtered == nil {
		normalised.Filtered = []Filtered{}
	}

	return json.Marshal(normalised)
}

// FilteredDiscoverer is a Discoverer that can account for what it removed.
//
// Filtering happens at discovery, where the file is still available, so a
// discoverer is the only place that can honour an inline directive. Callers use
// this to state the skips in a report rather than under-reporting the run.
type FilteredDiscoverer interface {
	Discoverer

	// Filtered returns the references removed before the registry was
	// consulted, in file order.
	Filtered() []Filtered
}
