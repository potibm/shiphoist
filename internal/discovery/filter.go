package discovery

import (
	"context"
	"regexp"
	"slices"

	"github.com/potibm/shiphoist/internal/core"
)

// ExcludeDiscoverer drops references whose repository matches a pattern.
//
// The pattern is matched against the repository alone, never the tag or digest.
// Excluding "everything from this registry" or "everything in this namespace" is
// what the flag is for, and matching the full reference would make `^nginx$`
// silently never match, since the reference is really `nginx:1.25.0`.
type ExcludeDiscoverer struct {
	// Inner is the discoverer whose references are filtered. It may itself be
	// filtered, in which case its skips are reported too.
	Inner core.Discoverer

	// Pattern is matched against each repository. A nil pattern is a no-op, so
	// the decorator can be installed unconditionally.
	Pattern *regexp.Regexp

	excluded []core.Filtered
}

// Discover returns the references the inner discoverer found, minus the
// excluded ones.
func (d *ExcludeDiscoverer) Discover(ctx context.Context, filePath string) ([]core.ImageUpdate, error) {
	d.excluded = nil

	updates, err := d.Inner.Discover(ctx, filePath)
	if err != nil {
		return nil, err
	}

	if d.Pattern == nil {
		return updates, nil
	}

	kept := make([]core.ImageUpdate, 0, len(updates))

	for _, u := range updates {
		if d.Pattern.MatchString(u.ImageName) {
			d.excluded = append(d.excluded, core.Filtered{
				Image:      u.ImageName,
				LineNumber: u.LineNumber,
				Reason:     ReasonExcluded,
			})

			continue
		}

		kept = append(kept, u)
	}

	return kept, nil
}

// Filtered returns the excluded references together with anything the inner
// discoverer filtered, in file order.
//
// Reporting both here means a caller only has to ask the outermost discoverer,
// so a chain needs no walking to account for the whole run.
func (d *ExcludeDiscoverer) Filtered() []core.Filtered {
	all := make([]core.Filtered, 0, len(d.excluded))

	if inner, ok := d.Inner.(core.FilteredDiscoverer); ok {
		all = append(all, inner.Filtered()...)
	}

	all = append(all, d.excluded...)
	slices.SortStableFunc(all, func(a, b core.Filtered) int {
		return a.LineNumber - b.LineNumber
	})

	return all
}
