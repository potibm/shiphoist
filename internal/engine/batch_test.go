package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/potibm/shiphoist/internal/core"
)

// reference builds a discovered update at a distinct line, as the Compose
// discoverer would.
func reference(line int, service, image, tag, digest string) core.ImageUpdate {
	return core.ImageUpdate{
		FilePath:       "docker-compose.yml",
		LineNumber:     line,
		ServiceName:    service,
		ImageName:      image,
		OldTag:         tag,
		OldDigest:      digest,
		OriginalString: image + ":" + tag,
	}
}

func TestGroupUpdates_CollapsesIdenticalReferences(t *testing.T) {
	updates := []core.ImageUpdate{
		reference(231, "billedapparat", "ghcr.io/potibm/billedapparat", "0.11", ""),
		reference(259, "billedapparat-collector-mastodon", "ghcr.io/potibm/billedapparat", "0.11", ""),
		reference(291, "billedapparat-collector-bluesky", "ghcr.io/potibm/billedapparat", "0.11", ""),
		reference(377, "funkapparat", "ghcr.io/potibm/funkapparat", "0.2", ""),
	}

	groups := groupUpdates(updates)

	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}

	if groups[0].size() != 3 {
		t.Errorf("expected 3 members for the shared image, got %d", groups[0].size())
	}

	if groups[1].size() != 1 {
		t.Errorf("expected 1 member for the unique image, got %d", groups[1].size())
	}
}

// A pinned and an unpinned reference to the same image:tag take different
// paths through the fetcher, so they must never share a lookup.
func TestGroupUpdates_KeepsPinnedAndUnpinnedApart(t *testing.T) {
	const pinned = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

	updates := []core.ImageUpdate{
		reference(4, "db", "postgres", "16", ""),
		reference(9, "cache", "postgres", "16", pinned),
	}

	groups := groupUpdates(updates)

	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}

	for _, group := range groups {
		if group.size() != 1 {
			t.Errorf("expected single-member groups, got %d members", group.size())
		}
	}
}

func TestGroupUpdates_DistinctTagsAreNotMerged(t *testing.T) {
	updates := []core.ImageUpdate{
		reference(4, "db", "postgres", "16", ""),
		reference(9, "cache", "postgres", "16-alpine", ""),
	}

	if groups := groupUpdates(updates); len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
}

func TestGroupUpdates_Empty(t *testing.T) {
	if got := groupUpdates(nil); len(got) != 0 {
		t.Errorf("expected no groups, got %d", len(got))
	}
}

func TestFetchGroup_Label(t *testing.T) {
	t.Run("single member names its service", func(t *testing.T) {
		group := fetchGroup{Members: []core.ImageUpdate{reference(4, "web", "nginx", "1.25.0", "")}}

		if got := group.label(); got != "[web] nginx" {
			t.Errorf("label() = %q, want %q", got, "[web] nginx")
		}
	})

	t.Run("shared group names the image and the reference count", func(t *testing.T) {
		group := fetchGroup{Members: []core.ImageUpdate{
			reference(4, "a", "nginx", "1.25.0", ""),
			reference(9, "b", "nginx", "1.25.0", ""),
		}}

		want := "nginx (shared by 2 references)"
		if got := group.label(); got != want {
			t.Errorf("label() = %q, want %q", got, want)
		}
	})
}

// The fan-out must give every member its own declaration site. Returning the
// representative as-is would rewrite all three lines to the first service's.
func TestApplyResultToAll_PreservesEachDeclarationSite(t *testing.T) {
	members := []core.ImageUpdate{
		reference(231, "billedapparat", "ghcr.io/potibm/billedapparat", "0.11", ""),
		reference(259, "billedapparat-collector-mastodon", "ghcr.io/potibm/billedapparat", "0.11", ""),
		reference(291, "billedapparat-collector-bluesky", "ghcr.io/potibm/billedapparat", "0.11", ""),
	}

	result := members[0]
	result.NewTag = "0.12"
	result.NewDigest = "sha256:new"
	result.CurrentDigest = "sha256:old"
	result.UpdateType = core.UpdateTypePatch
	result.Selected = true

	applied := applyResultToAll(result, members)

	if len(applied) != 3 {
		t.Fatalf("expected 3 applied updates, got %d", len(applied))
	}

	wantLines := []int{231, 259, 291}
	wantServices := []string{"billedapparat", "billedapparat-collector-mastodon", "billedapparat-collector-bluesky"}

	for i, update := range applied {
		if update.LineNumber != wantLines[i] {
			t.Errorf("member %d: expected line %d, got %d", i, wantLines[i], update.LineNumber)
		}

		if update.ServiceName != wantServices[i] {
			t.Errorf("member %d: expected service %q, got %q", i, wantServices[i], update.ServiceName)
		}

		if update.NewTag != "0.12" || update.NewDigest != "sha256:new" {
			t.Errorf("member %d: expected the resolved result, got %s@%s", i, update.NewTag, update.NewDigest)
		}

		if !update.Selected {
			t.Errorf("member %d: expected Selected to be carried over", i)
		}
	}

	if applied[0].Key() == applied[1].Key() {
		t.Error("expected distinct keys so the patcher targets separate lines")
	}
}

func TestApplyResultToAll_KeepsMemberDigests(t *testing.T) {
	const memberDigest = "sha256:member-pinned"

	members := []core.ImageUpdate{
		reference(4, "db", "postgres", "16", ""),
		reference(9, "cache", "postgres", "16", memberDigest),
	}

	applied := applyResultToAll(core.ImageUpdate{NewTag: "16.4", NewDigest: "sha256:new"}, members)

	if applied[0].OldDigest != "" {
		t.Errorf("expected the unpinned member to keep an empty digest, got %q", applied[0].OldDigest)
	}

	if applied[1].OldDigest != memberDigest {
		t.Errorf("expected the pinned member to keep %q, got %q", memberDigest, applied[1].OldDigest)
	}
}

// End-to-end through the pipeline: eight references, one lookup, eight
// correctly addressed updates.
func TestProcessFile_SharedImageFetchedOnceButPatchedEverywhere(t *testing.T) {
	const (
		serviceCount = 8
		lineBase     = 231
	)

	updates := make([]core.ImageUpdate, 0, serviceCount)
	for i := range serviceCount {
		updates = append(updates, core.ImageUpdate{
			FilePath:       testFilePath,
			LineNumber:     lineBase + i*16,
			ServiceName:    "billedapparat-collector",
			ImageName:      "ghcr.io/potibm/billedapparat",
			OldTag:         "0.11",
			OriginalString: "ghcr.io/potibm/billedapparat:0.11",
		})
	}

	discoverer := &stubDiscoverer{updates: updates}
	fetcher := &stubFetcher{}
	prompter := &stubPrompter{}
	patcher := &stubPatcher{}

	pipeline, out := newTestPipeline(discoverer, fetcher, prompter, patcher)

	got, err := pipeline.ProcessFile(context.Background(), testFilePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if calls := fetcher.calls.Load(); calls != 1 {
		t.Errorf("expected 1 registry lookup for 8 identical references, got %d", calls)
	}

	if len(got) != serviceCount {
		t.Fatalf("expected %d updates to patch, got %d", serviceCount, len(got))
	}

	if len(patcher.applied) != serviceCount {
		t.Errorf("expected %d patched updates, got %d", serviceCount, len(patcher.applied))
	}

	for i, update := range patcher.applied {
		wantLine := lineBase + i*16
		if update.LineNumber != wantLine {
			t.Errorf("patch %d: expected line %d, got %d", i, wantLine, update.LineNumber)
		}
	}

	if !strings.Contains(out.String(), "1 unique image (8 references)") {
		t.Errorf("expected the summary to explain the deduplication, got:\n%s", out.String())
	}
}

func TestProcessFile_SummaryReportsPlainCountWhenNothingIsShared(t *testing.T) {
	discoverer := &stubDiscoverer{updates: testUpdates()}
	fetcher := &stubFetcher{}
	prompter := &stubPrompter{}
	patcher := &stubPatcher{}

	pipeline, out := newTestPipeline(discoverer, fetcher, prompter, patcher)

	if _, err := pipeline.ProcessFile(context.Background(), testFilePath); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out.String(), "Checked 3 images") {
		t.Errorf("expected a plain count, got:\n%s", out.String())
	}
}

func TestProgressSummary(t *testing.T) {
	tests := []struct {
		name       string
		groups     int
		references int
		skipped    int
		expected   string
	}{
		{
			name:     "all distinct",
			groups:   3,
			expected: "✅ Checked 3 images in ",
		},
		{
			name:       "deduplicated",
			groups:     2,
			references: 8,
			expected:   "✅ Checked 2 unique images (8 references) in ",
		},
		{
			name:       "with skips",
			groups:     3,
			references: 3,
			skipped:    2,
			expected:   "✅ Checked 3 images in ",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			references := tc.references
			if references == 0 {
				references = tc.groups
			}

			got := progressSummary(tc.groups, references, 0, tc.skipped)

			if !strings.HasPrefix(got, tc.expected) {
				t.Errorf("progressSummary() = %q, want prefix %q", got, tc.expected)
			}

			if tc.skipped > 0 && !strings.Contains(got, "2 skipped") {
				t.Errorf("expected the skip count in %q", got)
			}
		})
	}
}

func TestSortByDeclarationOrder(t *testing.T) {
	updates := []core.ImageUpdate{
		{FilePath: "b.yml", LineNumber: 2},
		{FilePath: "a.yml", LineNumber: 9},
		{FilePath: "a.yml", LineNumber: 1},
		{FilePath: "a.yml", LineNumber: 5},
	}

	sortByDeclarationOrder(updates)

	want := []struct {
		path string
		line int
	}{
		{"a.yml", 1},
		{"a.yml", 5},
		{"a.yml", 9},
		{"b.yml", 2},
	}

	for i, expected := range want {
		if updates[i].FilePath != expected.path || updates[i].LineNumber != expected.line {
			t.Errorf("position %d: expected %s:%d, got %s:%d",
				i, expected.path, expected.line, updates[i].FilePath, updates[i].LineNumber)
		}
	}
}
