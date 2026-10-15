// Package prompting decides which of the resolved updates to apply when there
// is no user to ask.
package prompting

import "github.com/potibm/shiphoist/internal/core"

// Apply is the non-interactive selector behind --yes. It applies every update
// the fetcher declared safe, and never asks.
//
// The cap behaves differently here than in the interactive table, and the
// difference is deliberate. Interactively, an update above the cap is shown but
// left unchecked, so the user can see it exists and opt into it. With no user
// there is nobody to opt, so "not pre-selected" can only mean "not applied".
type Apply struct {
	// MaxUpdate is the highest severity that may be applied. The zero value
	// means no cap.
	MaxUpdate core.UpdateType
}

func (a Apply) SelectUpdates(updates []core.ImageUpdate) ([]core.ImageUpdate, error) {
	return filterSelectable(updates, a.MaxUpdate), nil
}

// filterSelectable keeps the updates that may be applied without being asked
// about, preserving order.
//
// Two independent gates have to pass, and neither is enough on its own.
//
// The severity cap is what --mode configures. The Selected flag is the
// fetcher's verdict that a reference may be updated unprompted: a major is
// never pre-selected, which is what keeps it out of a --yes run even when no
// cap is configured. Checking it again here rather than trusting the caller
// puts the guarantee next to the code that depends on it, so a change upstream
// cannot silently widen what --yes applies.
//
// Without --mode the ceiling still stops below a major, so the rule holds for
// an update a future fetcher marks selectable by mistake.
func filterSelectable(updates []core.ImageUpdate, maxUpdate core.UpdateType) []core.ImageUpdate {
	ceiling := severityCeiling(maxUpdate)

	kept := make([]core.ImageUpdate, 0, len(updates))

	for _, u := range updates {
		if !u.Selected || core.Severity(u.UpdateType) > ceiling {
			continue
		}

		kept = append(kept, u)
	}

	return kept
}

// severityCeiling resolves the cap to the rank it allows.
//
// A major is never reachable, whichever cap is set. `--mode major` is not an
// opt-in to apply majors automatically: it only stops the interactive table from
// capping the visible choice. Under --yes there is nobody to check a box, so the
// ceiling stops below a major here too.
func severityCeiling(maxUpdate core.UpdateType) int {
	if maxUpdate == "" {
		return core.SeverityMinor
	}

	ceiling := core.Severity(maxUpdate)
	if ceiling > core.SeverityMinor {
		return core.SeverityMinor
	}

	return ceiling
}
