// Package prompting decides which of the resolved updates to apply when there
// is no user to ask.
package prompting

import "github.com/potibm/shiphoist/internal/core"

// Apply is the non-interactive selector behind --yes. It applies every update
// within the cap, and never asks.
//
// The cap behaves differently here than in the interactive table, and the
// difference is deliberate. Interactively, an update above the cap is shown but
// left unchecked, so the user can see it exists and opt into it. With no user
// there is nobody to opt, so "not pre-selected" can only mean "not applied".
//
// It relies on the pipeline offering only updates the registry already marked
// Selected, so it filters by severity alone and does not re-check that.
type Apply struct {
	// MaxUpdate is the highest severity that may be applied. The zero value
	// means no cap.
	MaxUpdate core.UpdateType
}

func (a Apply) SelectUpdates(updates []core.ImageUpdate) ([]core.ImageUpdate, error) {
	return filterBySeverity(updates, a.MaxUpdate), nil
}

// filterBySeverity keeps the updates at or below the cap, preserving order.
//
// An unset cap keeps everything, so the filter is a no-op rather than a
// comparison against a sentinel that an unexpected update type could slip under.
func filterBySeverity(updates []core.ImageUpdate, maxUpdate core.UpdateType) []core.ImageUpdate {
	if maxUpdate == "" {
		return updates
	}

	ceiling := core.Severity(maxUpdate)

	kept := make([]core.ImageUpdate, 0, len(updates))

	for _, u := range updates {
		if core.Severity(u.UpdateType) <= ceiling {
			kept = append(kept, u)
		}
	}

	return kept
}
