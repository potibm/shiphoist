package tui

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/potibm/shiphoist/internal/core"
	"github.com/potibm/shiphoist/internal/ui"
)

const (
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

// safeToApply reports whether a group is pre-selected under the given cap.
//
// A major update is never pre-selected, whatever the cap says. The cap is a
// ceiling a user asked for, so it can only tighten the automatic selection; it
// must never be able to reopen the major-update safety floor.
func (g updateGroup) safeToApply(maxUpdate core.UpdateType) bool {
	severity := core.Severity(g.Head.UpdateType)

	return severity < core.Severity(core.UpdateTypeMajor) &&
		severity <= core.Severity(maxUpdate)
}

// formRunner renders a built form and returns once it is confirmed or aborted.
type formRunner func(form *huh.Form) error

// runInteractive renders the form against the real terminal.
func runInteractive(form *huh.Form) error {
	return form.Run()
}

// runFrom renders the form in accessible mode, drawing to out and reading keys
// from in.
//
// Accessible mode is the only way to drive a huh form without a terminal: it
// prints a numbered list and reads line input instead of raw keystrokes. A
// blank line confirms whatever is currently selected, which is what lets a test
// assert on the defaults shiphoist preselects.
func runFrom(in io.Reader, out io.Writer) formRunner {
	return func(form *huh.Form) error {
		form.WithAccessible(true).WithInput(in).WithOutput(out)

		return form.Run()
	}
}

type HuhPrompter struct {
	// run renders each form. Defaults to runInteractive.
	run formRunner

	// width is the column budget for the table. Zero means detect it.
	width int

	// maxUpdate caps the magnitude that may be pre-selected. The zero value
	// means no cap beyond the built-in rule that a major is never preselected.
	maxUpdate core.UpdateType
}

// NewHuhPrompter returns a prompter that caps pre-selection at maxUpdate.
func NewHuhPrompter(maxUpdate core.UpdateType) *HuhPrompter {
	return &HuhPrompter{run: runInteractive, maxUpdate: maxUpdate}
}

func (h *HuhPrompter) SelectUpdates(updates []core.ImageUpdate) ([]core.ImageUpdate, error) {
	if len(updates) == 0 {
		return updates, nil
	}

	groups := sortGroupsByRelevance(groupUpdates(updates))
	rows := rowsFor(groups, h.tableWidth())
	ceiling := h.selectCap()

	var (
		selectedKeys []string
		options      = make([]huh.Option[string], 0, len(groups))
	)

	for i, group := range groups {
		options = append(options, huh.NewOption(rows[i], group.Key).Selected(group.safeToApply(ceiling)))
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

	if err := h.run(form); err != nil {
		return nil, err
	}

	return h.resolveGroups(groups, selectedKeys)
}

// selectCap is the highest severity that may be preselected. An unset cap falls
// back to major, which reproduces the original rule that a major is never
// preselected.
func (h *HuhPrompter) selectCap() core.UpdateType {
	if h.maxUpdate == "" {
		return core.UpdateTypeMajor
	}

	return h.maxUpdate
}

// tableWidth is the column budget available for a row.
func (h *HuhPrompter) tableWidth() int {
	if h.width > 0 {
		return h.width
	}

	// huh draws on stderr, so its rows are budgeted against stderr's width.
	return ui.TerminalWidth(os.Stderr) - huhChrome
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

	if err := h.run(form); err != nil {
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
		return core.Severity(ordered[i].Head.UpdateType) > core.Severity(ordered[j].Head.UpdateType)
	})

	return ordered
}

// rowsFor renders the group table, truncating to the given width so no row
// wraps.
func rowsFor(groups []updateGroup, width int) []string {
	rows := make([]ui.Row, 0, len(groups))
	for _, group := range groups {
		rows = append(rows, rowFor(group))
	}

	return ui.RenderRows(rows, width)
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
