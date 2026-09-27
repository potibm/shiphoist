package core

import "encoding/json"

// Report is the outcome of one run over one file.
//
// It exists so a caller can describe a run without parsing human-facing output:
// the CLI renders it, a future --json mode serialises it verbatim, and a CI job
// can branch on Written or exit non-zero on Failures.
type Report struct {
	// File is the file that was processed.
	File string `json:"file"`

	// Updates are the changes that were applied, in declaration order.
	Updates []ImageUpdate `json:"updates"`

	// Failures are the references that could not be resolved. A failure is
	// reported and skipped; it never aborts the run.
	Failures []Failure `json:"failures"`

	// Checked is the number of distinct registry lookups made, which is lower
	// than References whenever references were deduplicated.
	Checked int `json:"checked"`

	// References is the number of declarations discovered in the file.
	References int `json:"references"`

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

// reportAlias breaks the recursion into MarshalJSON.
type reportAlias Report

// MarshalJSON guarantees Updates and Failures serialise as arrays, never null,
// however the report was built.
//
// A nil slice would otherwise reach a consumer as `null`, so every machine
// reader would have to handle both shapes. Enforcing it here rather than in a
// constructor means the guarantee travels with the type.
func (r Report) MarshalJSON() ([]byte, error) {
	normalised := reportAlias(r)

	if normalised.Updates == nil {
		normalised.Updates = []ImageUpdate{}
	}

	if normalised.Failures == nil {
		normalised.Failures = []Failure{}
	}

	return json.Marshal(normalised)
}
