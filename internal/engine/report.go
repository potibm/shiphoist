package engine

import (
	"github.com/potibm/shiphoist/internal/core"
	"github.com/potibm/shiphoist/internal/ui"
)

// newReport seeds a report for a run over filePath.
func newReport(filePath string, references int) *core.Report {
	return &core.Report{
		File:       filePath,
		References: references,
	}
}

// coreFailures converts progress failures into report failures.
//
// The error is flattened to a string here: an error value has no useful JSON
// representation, and the progress label already carries the image reference.
func coreFailures(failures []ui.Failure) []core.Failure {
	converted := make([]core.Failure, 0, len(failures))

	for _, failure := range failures {
		converted = append(converted, core.Failure{
			Image:   failure.Label,
			Message: failure.Err.Error(),
		})
	}

	return converted
}
