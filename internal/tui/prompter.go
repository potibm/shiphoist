package tui

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/potibm/shiphoist/internal/core"
	"github.com/potibm/shiphoist/internal/ui"
)

const (
	scoreMajor = 4
	scoreMinor = 3
	scorePatch = 2
	scoreNone  = 1
	scoreOther = 0

	// huhChrome is the width huh spends on its own cursor, checkbox and
	// padding. Rows are shortened by this much so the surrounding renderer
	// never has to wrap them.
	huhChrome = 4

	countSuffix = "×%d"
	arrow       = " → "
)

// updateGroup collapses updates that resolve identically, so a repository used
// by many services occupies one row instead of one row per service.
type updateGroup struct {
	// Key identifies the group to the selection widget.
	Key string

	// Members are every update in the group, each keeping its own line.
	Members []core.ImageUpdate

	// Head is the member whose fields describe the group.
	Head core.ImageUpdate
}

// safeToApply reports whether a group is pre-selected. Major updates are never
// pre-selected, matching the safety rule for ungrouped rows.
func (g updateGroup) safeToApply() bool {
	return g.Head.UpdateType != core.UpdateTypeMajor
}

type HuhPrompter struct{}

func NewHuhPrompter() *HuhPrompter {
	return &HuhPrompter{}
}

func (h *HuhPrompter) SelectUpdates(updates []core.ImageUpdate) ([]core.ImageUpdate, error) {
	if len(updates) == 0 {
		return updates, nil
	}

	groups := sortGroupsByRelevance(groupUpdates(updates))
	rows := rowsFor(groups)

	var (
		selectedKeys []string
		options      = make([]huh.Option[string], 0, len(groups))
	)

	for i, group := range groups {
		options = append(options, huh.NewOption(rows[i], group.Key).Selected(group.safeToApply()))
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title(mainTitle(groups)).
				Description("Space toggles · ctrl+a selects all or none · enter confirms").
				Options(options...).
				Value(&selectedKeys),
		),
	)

	if err := form.Run(); err != nil {
		return nil, err
	}

	return h.resolveGroups(groups, selectedKeys)
}

// resolveGroups expands the selected groups back into individual updates.
func (h *HuhPrompter) resolveGroups(groups []updateGroup, selectedKeys []string) ([]core.ImageUpdate, error) {
	selected := make(map[string]struct{}, len(selectedKeys))
	for _, key := range selectedKeys {
		selected[key] = struct{}{}
	}

	var resolved []core.ImageUpdate

	for _, group := range groups {
		if _, ok := selected[group.Key]; !ok {
			continue
		}

		members, err := h.narrowGroup(group)
		if err != nil {
			return nil, err
		}

		resolved = append(resolved, members...)
	}

	return resolved, nil
}

// narrowGroup asks which services of a shared update to apply. Groups of one
// need no second prompt.
func (h *HuhPrompter) narrowGroup(group updateGroup) ([]core.ImageUpdate, error) {
	if len(group.Members) == 1 {
		return group.Members, nil
	}

	var (
		selectedKeys []string
		options      = make([]huh.Option[string], 0, len(group.Members))
	)

	for _, member := range group.Members {
		options = append(options, huh.NewOption(member.ServiceName, member.Key()).Selected(true))
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title(drillDownTitle(group)).
				Description("Every service is pre-selected. Deselect the ones to skip.").
				Options(options...).
				Value(&selectedKeys),
		),
	)

	if err := form.Run(); err != nil {
		return nil, err
	}

	return filterByKeys(group.Members, selectedKeys), nil
}

// groupUpdates collapses updates that would produce an identical registry
// result. The service name is deliberately not part of the key: it is the
// field that legitimately varies between otherwise identical references.
func groupUpdates(updates []core.ImageUpdate) []updateGroup {
	groups := make([]updateGroup, 0, len(updates))
	seen := make(map[string]int, len(updates))

	for _, update := range updates {
		key := groupKey(update)

		if index, ok := seen[key]; ok {
			groups[index].Members = append(groups[index].Members, update)

			continue
		}

		seen[key] = len(groups)
		groups = append(groups, updateGroup{
			Key:     update.Key(),
			Members: []core.ImageUpdate{update},
			Head:    update,
		})
	}

	return groups
}

// groupKey identifies a group by everything the row will display, so two
// references only share a row when they would also produce the same patch.
func groupKey(u core.ImageUpdate) string {
	return strings.Join([]string{u.ImageName, u.OldTag, u.NewTag, u.NewDigest, string(u.UpdateType)}, "\x00")
}

// sortGroupsByRelevance orders groups most- to least-interesting without
// mutating the caller's slice.
func sortGroupsByRelevance(groups []updateGroup) []updateGroup {
	ordered := make([]updateGroup, len(groups))
	copy(ordered, groups)

	sort.SliceStable(ordered, func(i, j int) bool {
		return scoreUpdateType(ordered[i].Head.UpdateType) > scoreUpdateType(ordered[j].Head.UpdateType)
	})

	return ordered
}

// rowsFor renders the group table, truncating to the terminal so no row wraps.
func rowsFor(groups []updateGroup) []string {
	rows := make([]ui.Row, 0, len(groups))
	for _, group := range groups {
		rows = append(rows, rowFor(group))
	}

	return ui.RenderRows(rows, ui.TerminalWidth(os.Stderr)-huhChrome)
}

func rowFor(group updateGroup) ui.Row {
	head := group.Head

	return ui.Row{
		Marker:  markerFor(head.UpdateType),
		Name:    groupName(group),
		Image:   head.ImageName,
		Version: versionFor(head),
		Kind:    kindFor(head.UpdateType),
	}
}

// groupName names the group. A shared update has no single owning service, so
// it is labelled with a count instead of a misleading single name.
func groupName(group updateGroup) string {
	if len(group.Members) == 1 {
		return group.Members[0].ServiceName
	}

	return fmt.Sprintf(countSuffix, len(group.Members))
}

// versionFor shows the tag transition, collapsing to a single tag when the tag
// did not change and only the digest is being pinned.
func versionFor(u core.ImageUpdate) string {
	if u.NewTag == "" || u.NewTag == u.OldTag {
		return u.OldTag
	}

	return u.OldTag + arrow + u.NewTag
}

// markerFor returns the coloured severity marker promised by the CLI output
// conventions: blue for pinning, green for patch, yellow for minor, red for
// major.
func markerFor(t core.UpdateType) string {
	switch t {
	case core.UpdateTypeMajor:
		return "🔴"
	case core.UpdateTypeMinor:
		return "🟡"
	case core.UpdateTypePatch:
		return "🟢"
	case core.UpdateTypeNone:
		return "🔵"
	default:
		return "⚪"
	}
}

func kindFor(t core.UpdateType) string {
	switch t {
	case core.UpdateTypeMajor:
		return "major"
	case core.UpdateTypeMinor:
		return "minor"
	case core.UpdateTypePatch:
		return "patch"
	case core.UpdateTypeNone:
		return "pin"
	default:
		return ""
	}
}

func mainTitle(groups []updateGroup) string {
	shared := 0

	for _, group := range groups {
		if len(group.Members) > 1 {
			shared++
		}
	}

	title := "🚢 Which updates should be applied?"
	if shared > 0 {
		title += fmt.Sprintf(" (%d shared)", shared)
	}

	return title
}

func drillDownTitle(group updateGroup) string {
	return fmt.Sprintf("%s %s · select services", group.Head.ImageName, versionFor(group.Head))
}

// filterByKeys returns the updates whose key was selected, preserving the
// order of updates.
func filterByKeys(updates []core.ImageUpdate, keys []string) []core.ImageUpdate {
	selected := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		selected[key] = struct{}{}
	}

	var filtered []core.ImageUpdate

	for _, u := range updates {
		if _, ok := selected[u.Key()]; ok {
			filtered = append(filtered, u)
		}
	}

	return filtered
}

func scoreUpdateType(t core.UpdateType) int {
	switch t {
	case core.UpdateTypeMajor:
		return scoreMajor
	case core.UpdateTypeMinor:
		return scoreMinor
	case core.UpdateTypePatch:
		return scorePatch
	case core.UpdateTypeNone:
		return scoreNone
	default:
		return scoreOther
	}
}
