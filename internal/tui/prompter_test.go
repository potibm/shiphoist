package tui

import (
	"hash/crc32"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/potibm/shiphoist/internal/core"
)

func testUpdate(service, image, oldTag, newTag string, updateType core.UpdateType) core.ImageUpdate {
	return core.ImageUpdate{
		FilePath:    "docker-compose.yml",
		LineNumber:  lineNumberFor(service),
		ServiceName: service,
		ImageName:   image,
		OldTag:      oldTag,
		NewTag:      newTag,
		NewDigest:   "sha256:new",
		UpdateType:  updateType,
		Selected:    true,
	}
}

// lineNumberFor gives each test service a distinct line, mirroring real
// discovery output where every reference has its own address.
func lineNumberFor(service string) int {
	return int(crc32.ChecksumIEEE([]byte(service))%1000) + 4
}

func TestScoreUpdateType(t *testing.T) {
	tests := []struct {
		name     string
		update   core.UpdateType
		expected int
	}{
		{name: "major outranks all", update: core.UpdateTypeMajor, expected: scoreMajor},
		{name: "minor", update: core.UpdateTypeMinor, expected: scoreMinor},
		{name: "patch", update: core.UpdateTypePatch, expected: scorePatch},
		{name: "none", update: core.UpdateTypeNone, expected: scoreNone},
		{name: "unknown falls back", update: core.UpdateType("weird"), expected: scoreOther},
		{name: "empty falls back", update: core.UpdateType(""), expected: scoreOther},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := scoreUpdateType(tc.update); got != tc.expected {
				t.Errorf("scoreUpdateType(%q) = %d, want %d", tc.update, got, tc.expected)
			}
		})
	}
}

func TestScoreUpdateType_Ordering(t *testing.T) {
	descending := []int{scoreMajor, scoreMinor, scorePatch, scoreNone, scoreOther}

	for i := 1; i < len(descending); i++ {
		if descending[i-1] <= descending[i] {
			t.Errorf("expected strict descending order, got %v", descending)
		}
	}
}

// Identical references across services collapse to a single row.
func TestGroupUpdates_CollapsesIdenticalReferences(t *testing.T) {
	updates := []core.ImageUpdate{
		testUpdate("billedapparat", "ghcr.io/potibm/billedapparat", "0.11", "0.12", core.UpdateTypePatch),
		testUpdate(
			"billedapparat-collector-mastodon",
			"ghcr.io/potibm/billedapparat",
			"0.11",
			"0.12",
			core.UpdateTypePatch,
		),
		testUpdate(
			"billedapparat-collector-bluesky",
			"ghcr.io/potibm/billedapparat",
			"0.11",
			"0.12",
			core.UpdateTypePatch,
		),
		testUpdate("funkapparat", "ghcr.io/potibm/funkapparat", "0.2", "0.3", core.UpdateTypeMinor),
	}

	groups := groupUpdates(updates)

	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}

	if len(groups[0].Members) != 3 {
		t.Errorf("expected 3 members in the shared group, got %d", len(groups[0].Members))
	}

	if len(groups[1].Members) != 1 {
		t.Errorf("expected 1 member in the unique group, got %d", len(groups[1].Members))
	}
}

// Grouping must never merge two references that would be patched differently.
func TestGroupUpdates_KeepsDistinctUpdatesApart(t *testing.T) {
	tests := []struct {
		name     string
		first    core.ImageUpdate
		second   core.ImageUpdate
		expected int
	}{
		{
			name:     "same repo, different tag",
			first:    testUpdate("db", "postgres", "16", "16.4", core.UpdateTypeMinor),
			second:   testUpdate("cache", "postgres", "16-alpine", "16.4-alpine", core.UpdateTypeMinor),
			expected: 2,
		},
		{
			name:     "same repo and tag, different target",
			first:    testUpdate("db", "postgres", "16", "16.4", core.UpdateTypeMinor),
			second:   testUpdate("cache", "postgres", "16", "16.5", core.UpdateTypeMinor),
			expected: 2,
		},
		{
			name:     "same repo and tag, different severity",
			first:    testUpdate("db", "postgres", "16", "17", core.UpdateTypeMajor),
			second:   testUpdate("cache", "postgres", "16", "16.4", core.UpdateTypeMinor),
			expected: 2,
		},
		{
			// A pinned and an unpinned reference resolve to the same written
			// result, so one row is correct here. They must still be fetched
			// separately, which the engine's fetch key enforces.
			name:     "same repo and tag, different existing digest",
			first:    testUpdate("db", "postgres", "16", "16.4", core.UpdateTypeMinor),
			second:   withOldDigest(testUpdate("cache", "postgres", "16", "16.4", core.UpdateTypeMinor)),
			expected: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			groups := groupUpdates([]core.ImageUpdate{tc.first, tc.second})

			if len(groups) != tc.expected {
				t.Errorf("expected %d groups, got %d", tc.expected, len(groups))
			}
		})
	}
}

func withOldDigest(u core.ImageUpdate) core.ImageUpdate {
	u.OldDigest = "sha256:already-pinned"

	return u
}

func TestGroupUpdates_GroupKeysAreUnique(t *testing.T) {
	updates := []core.ImageUpdate{
		testUpdate("a", "nginx", "1.0", "1.1", core.UpdateTypePatch),
		testUpdate("b", "nginx", "1.0", "1.1", core.UpdateTypePatch),
		testUpdate("c", "nginx", "1.0", "1.2", core.UpdateTypePatch),
	}

	seen := map[string]bool{}

	for _, group := range groupUpdates(updates) {
		if seen[group.Key] {
			t.Fatalf("duplicate group key %q", group.Key)
		}

		seen[group.Key] = true
	}
}

func TestUpdateGroup_SafeToApply(t *testing.T) {
	tests := []struct {
		name       string
		updateType core.UpdateType
		expected   bool
	}{
		{name: "patch is safe", updateType: core.UpdateTypePatch, expected: true},
		{name: "minor is safe", updateType: core.UpdateTypeMinor, expected: true},
		{name: "pinning is safe", updateType: core.UpdateTypeNone, expected: true},
		{name: "major is never pre-selected", updateType: core.UpdateTypeMajor, expected: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			group := updateGroup{Head: core.ImageUpdate{UpdateType: tc.updateType}}

			if got := group.safeToApply(); got != tc.expected {
				t.Errorf("safeToApply() = %v, want %v", got, tc.expected)
			}
		})
	}
}

func TestSortGroupsByRelevance(t *testing.T) {
	groups := []updateGroup{
		{Head: core.ImageUpdate{ImageName: "patch-me", UpdateType: core.UpdateTypePatch}},
		{Head: core.ImageUpdate{ImageName: "major-bump", UpdateType: core.UpdateTypeMajor}},
		{Head: core.ImageUpdate{ImageName: "minor-bump", UpdateType: core.UpdateTypeMinor}},
		{Head: core.ImageUpdate{ImageName: "pin-me", UpdateType: core.UpdateTypeNone}},
	}

	ordered := sortGroupsByRelevance(groups)

	wantOrder := []string{"major-bump", "minor-bump", "patch-me", "pin-me"}
	for i, image := range wantOrder {
		if ordered[i].Head.ImageName != image {
			t.Errorf("position %d: expected %q, got %q", i, image, ordered[i].Head.ImageName)
		}
	}
}

// The sort must not reorder the caller's slice.
func TestSortGroupsByRelevance_DoesNotMutateInput(t *testing.T) {
	groups := []updateGroup{
		{Head: core.ImageUpdate{ImageName: "a", UpdateType: core.UpdateTypePatch}},
		{Head: core.ImageUpdate{ImageName: "b", UpdateType: core.UpdateTypeMajor}},
	}

	_ = sortGroupsByRelevance(groups)

	if groups[0].Head.ImageName != "a" || groups[1].Head.ImageName != "b" {
		t.Errorf("input slice was reordered: %+v", groups)
	}
}

func TestSortGroupsByRelevance_Empty(t *testing.T) {
	if got := sortGroupsByRelevance(nil); len(got) != 0 {
		t.Errorf("expected empty result, got %d entries", len(got))
	}
}

func TestVersionFor(t *testing.T) {
	tests := []struct {
		name     string
		update   core.ImageUpdate
		expected string
	}{
		{
			name:     "tag changed",
			update:   core.ImageUpdate{OldTag: "0.2", NewTag: "0.3"},
			expected: "0.2 → 0.3",
		},
		{
			name:     "tag unchanged, digest pin only",
			update:   core.ImageUpdate{OldTag: "latest", NewTag: "latest"},
			expected: "latest",
		},
		{
			name:     "no target tag",
			update:   core.ImageUpdate{OldTag: "1.0.0"},
			expected: "1.0.0",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := versionFor(tc.update); got != tc.expected {
				t.Errorf("versionFor() = %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestMarkerFor(t *testing.T) {
	tests := []struct {
		name       string
		updateType core.UpdateType
		expected   string
	}{
		{name: "major", updateType: core.UpdateTypeMajor, expected: "🔴"},
		{name: "minor", updateType: core.UpdateTypeMinor, expected: "🟡"},
		{name: "patch", updateType: core.UpdateTypePatch, expected: "🟢"},
		{name: "pinning", updateType: core.UpdateTypeNone, expected: "🔵"},
		{name: "unknown", updateType: core.UpdateType("weird"), expected: "⚪"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := markerFor(tc.updateType); got != tc.expected {
				t.Errorf("markerFor(%q) = %q, want %q", tc.updateType, got, tc.expected)
			}
		})
	}
}

func TestKindFor(t *testing.T) {
	tests := []struct {
		name       string
		updateType core.UpdateType
		expected   string
	}{
		{name: "major", updateType: core.UpdateTypeMajor, expected: "major"},
		{name: "minor", updateType: core.UpdateTypeMinor, expected: "minor"},
		{name: "patch", updateType: core.UpdateTypePatch, expected: "patch"},
		{name: "pinning", updateType: core.UpdateTypeNone, expected: "pin"},
		{name: "unknown", updateType: core.UpdateType("weird"), expected: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := kindFor(tc.updateType); got != tc.expected {
				t.Errorf("kindFor(%q) = %q, want %q", tc.updateType, got, tc.expected)
			}
		})
	}
}

func TestGroupName(t *testing.T) {
	t.Run("single member shows its service", func(t *testing.T) {
		group := updateGroup{Members: []core.ImageUpdate{{ServiceName: "web"}}}

		if got := groupName(group); got != "web" {
			t.Errorf("groupName() = %q, want %q", got, "web")
		}
	})

	t.Run("shared group shows a count instead of a misleading name", func(t *testing.T) {
		group := updateGroup{Members: []core.ImageUpdate{
			{ServiceName: "billedapparat"},
			{ServiceName: "billedapparat-collector-mastodon"},
		}}

		if got := groupName(group); got != "×2" {
			t.Errorf("groupName() = %q, want %q", got, "×2")
		}
	})
}

func TestRowsFor_RendersTabularOutput(t *testing.T) {
	shared := testUpdate("billedapparat", "ghcr.io/potibm/billedapparat", "0.11", "0.12", core.UpdateTypePatch)
	sharedLine := shared.LineNumber
	sharedCollector := shared
	sharedCollector.ServiceName = "billedapparat-collector-mastodon"
	sharedCollector.LineNumber = sharedLine + 5

	groups := []updateGroup{
		{
			Head:    shared,
			Members: []core.ImageUpdate{shared, sharedCollector},
		},
		{
			Head: testUpdate("grafana", "grafana/grafana", "11.6.16", "11.6.17", core.UpdateTypeNone),
			Members: []core.ImageUpdate{
				testUpdate("grafana", "grafana/grafana", "11.6.16", "11.6.17", core.UpdateTypeNone),
			},
		},
	}

	rows := rowsFor(groups)

	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %v", len(rows), rows)
	}

	if !strings.Contains(rows[0], "×2") {
		t.Errorf("expected the shared group to show a count, got %q", rows[0])
	}

	if !strings.Contains(rows[0], "0.11 → 0.12") {
		t.Errorf("expected the version transition, got %q", rows[0])
	}

	if !strings.Contains(rows[1], "11.6.16") {
		t.Errorf("expected the grafana row, got %q", rows[1])
	}

	// The version column must begin at the same display column on every row,
	// which is what makes the table scannable.
	needles := []string{"0.11", "11.6.16"}
	if !alignedAt(rows, needles) {
		t.Errorf("expected the version column to be aligned:\n%q\n%q", rows[0], rows[1])
	}
}

// alignedAt reports whether each row contains its own needle at the same
// display column. Offsets are measured in display cells rather than bytes,
// because the markers are wide, multi-byte emoji.
func alignedAt(rows, needles []string) bool {
	if len(rows) != len(needles) {
		return false
	}

	offset := -1

	for i, row := range rows {
		index := strings.Index(row, needles[i])
		if index < 0 {
			return false
		}

		column := ansi.StringWidth(row[:index])
		if offset < 0 {
			offset = column
		} else if column != offset {
			return false
		}
	}

	return true
}

func TestMainTitle(t *testing.T) {
	shared := []updateGroup{
		{Members: []core.ImageUpdate{{}, {}}},
	}
	if got := mainTitle(shared); !strings.Contains(got, "1 shared") {
		t.Errorf("expected the shared count in the title, got %q", got)
	}

	unique := []updateGroup{{Members: []core.ImageUpdate{{}}}}
	if got := mainTitle(unique); strings.Contains(got, "shared") {
		t.Errorf("expected no shared count when nothing is shared, got %q", got)
	}
}

func TestDrillDownTitle(t *testing.T) {
	group := updateGroup{Head: testUpdate("db", "ghcr.io/potibm/billedapparat", "0.11", "0.12", core.UpdateTypePatch)}

	got := drillDownTitle(group)

	for _, want := range []string{"ghcr.io/potibm/billedapparat", "0.11 → 0.12", "select services"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in %q", want, got)
		}
	}
}

// Regression: the prompter used to key on ImageName, which excludes the tag.
// Two services sharing a repository therefore shared one selection identity,
// so deselecting one of them applied to both.
func TestFilterByKeys_SameRepoDifferentTagsStaysIndependent(t *testing.T) {
	updates := []core.ImageUpdate{
		{FilePath: "docker-compose.yml", LineNumber: 4, ServiceName: "db", ImageName: "postgres", OldTag: "16"},
		{
			FilePath:    "docker-compose.yml",
			LineNumber:  9,
			ServiceName: "cache",
			ImageName:   "postgres",
			OldTag:      "16-alpine",
		},
	}

	got := filterByKeys(updates, []string{updates[1].Key()})

	if len(got) != 1 {
		t.Fatalf("expected exactly 1 update, got %d: %+v", len(got), got)
	}

	if got[0].ServiceName != "cache" {
		t.Errorf("expected only the alpine service to be selected, got %q", got[0].ServiceName)
	}

	if got[0].OldTag != "16-alpine" {
		t.Errorf("expected the alpine tag to be selected, got %q", got[0].OldTag)
	}
}

// Collapsing a shared group must not collapse its members' identities: each
// service still needs its own key so the patcher targets the right line.
func TestFilterByKeys_GroupMembersStayIndependent(t *testing.T) {
	updates := []core.ImageUpdate{
		testUpdate("a", "ghcr.io/potibm/billedapparat", "0.11", "0.12", core.UpdateTypePatch),
		testUpdate("b", "ghcr.io/potibm/billedapparat", "0.11", "0.12", core.UpdateTypePatch),
		testUpdate("c", "ghcr.io/potibm/billedapparat", "0.11", "0.12", core.UpdateTypePatch),
	}

	groups := groupUpdates(updates)
	if len(groups) != 1 {
		t.Fatalf("expected the three references to share one group, got %d", len(groups))
	}

	got := filterByKeys(groups[0].Members, []string{updates[0].Key(), updates[2].Key()})

	if len(got) != 2 {
		t.Fatalf("expected 2 of 3 members, got %d", len(got))
	}

	if got[0].ServiceName != "a" || got[1].ServiceName != "c" {
		t.Errorf("expected services a and c, got %q and %q", got[0].ServiceName, got[1].ServiceName)
	}
}

func TestFilterByKeys_SelectAll(t *testing.T) {
	updates := []core.ImageUpdate{
		{FilePath: "docker-compose.yml", LineNumber: 4, ImageName: "nginx"},
		{FilePath: "docker-compose.yml", LineNumber: 8, ImageName: "redis"},
	}

	keys := []string{updates[0].Key(), updates[1].Key()}

	if got := filterByKeys(updates, keys); len(got) != 2 {
		t.Errorf("expected all updates to be returned, got %+v", got)
	}
}

func TestFilterByKeys_SelectNone(t *testing.T) {
	updates := []core.ImageUpdate{
		{FilePath: "docker-compose.yml", LineNumber: 4, ImageName: "nginx"},
	}

	if got := filterByKeys(updates, nil); len(got) != 0 {
		t.Errorf("expected no updates, got %+v", got)
	}
}

// A nil result must be safe to range over and report as "nothing selected".
func TestFilterByKeys_ReturnsNilNotEmptySlice(t *testing.T) {
	updates := []core.ImageUpdate{
		{FilePath: "docker-compose.yml", LineNumber: 4, ImageName: "nginx"},
	}

	if got := filterByKeys(updates, nil); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

// Filtering must follow the order it was given, not the order of the keys.
func TestFilterByKeys_PreservesInputOrder(t *testing.T) {
	updates := []core.ImageUpdate{
		{FilePath: "docker-compose.yml", LineNumber: 4, ImageName: "nginx"},
		{FilePath: "docker-compose.yml", LineNumber: 8, ImageName: "redis"},
		{FilePath: "docker-compose.yml", LineNumber: 12, ImageName: "postgres"},
	}

	got := filterByKeys(updates, []string{updates[2].Key(), updates[0].Key()})

	wantLines := []int{4, 12}
	for i, line := range wantLines {
		if got[i].LineNumber != line {
			t.Errorf("position %d: expected line %d, got %d", i, line, got[i].LineNumber)
		}
	}
}
