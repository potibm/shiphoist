package registry

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/go-containerregistry/pkg/v1/remote/transport"

	"github.com/potibm/shiphoist/internal/core"
)

type mockClient struct {
	getDigestFn func(ctx context.Context, ref string) (string, error)
	listTagsFn  func(ctx context.Context, repo string) ([]string, error)
}

func (m *mockClient) GetDigest(ctx context.Context, ref string) (string, error) {
	return m.getDigestFn(ctx, ref)
}

func (m *mockClient) ListTags(ctx context.Context, repo string) ([]string, error) {
	return m.listTagsFn(ctx, repo)
}

func TestDefaultFetcher_FetchUpdate_SemverUpgrade(t *testing.T) {
	// Mirrors the historical live-registry test for alpine, but with a fixed
	// tag list so the result is deterministic.
	testFetchUpdate(t, fetchUpdateTestCase{
		name:           "SemVer upgrade",
		imageName:      "alpine",
		oldTag:         "3.17.0",
		oldDigestRef:   "alpine:3.17.0",
		tags:           []string{"3.16.3", "3.17.0", "3.17.3", "3.18.4"},
		expectedNewTag: "3.18.4",
		expectedType:   core.UpdateTypeMinor,
		expectedMajor:  "",
	})
}

func TestDefaultFetcher_FetchUpdate_SuffixlessPicksAbsoluteLatest(t *testing.T) {
	// A tag without a flavor suffix matches every other suffixless tag, so the
	// absolute latest wins regardless of magnitude.
	testFetchUpdate(t, fetchUpdateTestCase{
		name:           "suffixless absolute latest",
		imageName:      "alpine",
		oldTag:         "3.17.0",
		oldDigestRef:   "alpine:3.17.0",
		tags:           []string{"3.17.0", "3.18.4"},
		expectedNewTag: "3.18.4",
		expectedType:   core.UpdateTypeMinor,
		expectedMajor:  "",
	})
}

func TestDefaultFetcher_FetchUpdate_NonSemverFallback(t *testing.T) {
	mock := &mockClient{
		getDigestFn: func(ctx context.Context, ref string) (string, error) {
			return "sha256:latest-digest", nil
		},
		listTagsFn: func(ctx context.Context, repo string) ([]string, error) {
			return []string{"latest", "stable", "1.25.0"}, nil
		},
	}

	fetcher := NewDefaultFetcher(mock)

	current := core.ImageUpdate{
		ImageName: "nginx",
		OldTag:    "latest",
	}

	updated, err := fetcher.FetchUpdate(context.Background(), current)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if updated.NewTag != "latest" {
		t.Errorf("expected tag to remain 'latest', got %q", updated.NewTag)
	}

	if updated.NewDigest != "sha256:latest-digest" {
		t.Errorf("expected the floating tag to be pinned, got digest %q", updated.NewDigest)
	}

	if !updated.Selected {
		t.Error("expected Selected to be true for an unpinned floating tag")
	}
}

func TestDefaultFetcher_ListTags(t *testing.T) {
	mock := &mockClient{
		getDigestFn: func(ctx context.Context, ref string) (string, error) {
			return "sha256:unused", nil
		},
		listTagsFn: func(ctx context.Context, repo string) ([]string, error) {
			return []string{"3.18", "3.18.4", "latest"}, nil
		},
	}

	fetcher := NewDefaultFetcher(mock)

	tags, err := fetcher.listTags(context.Background(), "alpine")
	if err != nil {
		t.Fatalf("unexpected error listing tags: %v", err)
	}

	// "latest" is not valid SemVer and is deliberately dropped, so that
	// floating tags never win an automatic version comparison.
	expected := []string{"3.18", "3.18.4"}
	if len(tags) != len(expected) {
		t.Fatalf("expected %d tags, got %d", len(expected), len(tags))
	}

	for i, raw := range expected {
		if tags[i].Raw != raw {
			t.Errorf("tag %d: expected %q, got %q", i, raw, tags[i].Raw)
		}
	}
}

func TestDefaultFetcher_FetchUpdate_MissingTag(t *testing.T) {
	mock := &mockClient{
		getDigestFn: func(ctx context.Context, ref string) (string, error) {
			return "", &transport.Error{StatusCode: http.StatusNotFound}
		},
		listTagsFn: func(ctx context.Context, repo string) ([]string, error) {
			return []string{"0.9.5", "1.0.0", "1.1.0", "1.2.0"}, nil
		},
	}

	fetcher := NewDefaultFetcher(mock)

	current := core.ImageUpdate{
		ImageName: "example/repo",
		OldTag:    "0.9.0",
	}

	updated, err := fetcher.FetchUpdate(context.Background(), current)
	if err != nil {
		t.Fatalf("expected no error for missing tag, got: %v", err)
	}

	if !updated.OldTagMissing {
		t.Error("expected OldTagMissing to be true")
	}

	if updated.NewTag != "0.9.5" {
		t.Errorf("expected NewTag to be 0.9.5 (same-major), got %s", updated.NewTag)
	}

	if updated.UpdateType != core.UpdateTypePatch {
		t.Errorf("expected UpdateType to be patch, got %s", updated.UpdateType)
	}

	if !updated.Selected {
		t.Error("expected Selected to be true for same-major update")
	}

	if updated.MajorTag != "1.2.0" {
		t.Errorf("expected MajorTag to be 1.2.0, got %s", updated.MajorTag)
	}

	t.Logf("Successfully handled missing tag: 0.9.0 -> %s (safe), major %s available", updated.NewTag, updated.MajorTag)
}

func TestDefaultFetcher_FetchUpdate_MissingTag_NoNewerVersion(t *testing.T) {
	mock := &mockClient{
		getDigestFn: func(ctx context.Context, ref string) (string, error) {
			return "", &transport.Error{StatusCode: http.StatusNotFound}
		},
		listTagsFn: func(ctx context.Context, repo string) ([]string, error) {
			return []string{"0.8.0", "0.9.0", "1.0.0"}, nil
		},
	}

	fetcher := NewDefaultFetcher(mock)

	current := core.ImageUpdate{
		ImageName: "example/repo",
		OldTag:    "0.9.0",
	}

	updated, err := fetcher.FetchUpdate(context.Background(), current)
	if err != nil {
		t.Fatalf("expected no error for missing tag, got: %v", err)
	}

	if !updated.OldTagMissing {
		t.Error("expected OldTagMissing to be true")
	}

	if updated.NewTag != "0.9.0" {
		t.Errorf("expected NewTag to remain 0.9.0, got %s", updated.NewTag)
	}

	if updated.Selected {
		t.Error("expected Selected to be false (no same-major update)")
	}

	if updated.MajorTag != "1.0.0" {
		t.Errorf("expected MajorTag to be 1.0.0, got %s", updated.MajorTag)
	}

	t.Logf("Successfully handled missing tag with no same-major update, major %s available", updated.MajorTag)
}

func TestDefaultFetcher_FetchUpdate_Non404Error(t *testing.T) {
	mock := &mockClient{
		getDigestFn: func(ctx context.Context, ref string) (string, error) {
			return "", fmt.Errorf("network error")
		},
		listTagsFn: func(ctx context.Context, repo string) ([]string, error) {
			return []string{"1.0.0"}, nil
		},
	}

	fetcher := NewDefaultFetcher(mock)

	current := core.ImageUpdate{
		ImageName: "example/repo",
		OldTag:    "0.9.0",
	}

	_, err := fetcher.FetchUpdate(context.Background(), current)
	if err == nil {
		t.Fatal("expected error for non-404 error, got nil")
	}

	t.Logf("Correctly failed on non-404 error: %v", err)
}

func TestDefaultFetcher_FetchUpdate_CurrentDigest(t *testing.T) {
	mock := &mockClient{
		getDigestFn: func(ctx context.Context, ref string) (string, error) {
			if ref == "example/repo:1.0.0" {
				return "sha256:current123", nil
			}

			return "sha256:new456", nil
		},
		listTagsFn: func(ctx context.Context, repo string) ([]string, error) {
			return []string{"1.0.0", "1.1.0"}, nil
		},
	}

	fetcher := NewDefaultFetcher(mock)

	current := core.ImageUpdate{
		ImageName: "example/repo",
		OldTag:    "1.0.0",
	}

	updated, err := fetcher.FetchUpdate(context.Background(), current)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if updated.CurrentDigest != "sha256:current123" {
		t.Errorf("expected CurrentDigest to be sha256:current123, got %s", updated.CurrentDigest)
	}

	if updated.NewDigest != "sha256:new456" {
		t.Errorf("expected NewDigest to be sha256:new456, got %s", updated.NewDigest)
	}

	t.Logf("CurrentDigest: %s, NewDigest: %s", updated.CurrentDigest, updated.NewDigest)
}

func TestDefaultFetcher_FetchUpdate_NoCompatibleTags(t *testing.T) {
	mock := &mockClient{
		getDigestFn: func(ctx context.Context, ref string) (string, error) {
			return "sha256:abc", nil
		},
		listTagsFn: func(ctx context.Context, repo string) ([]string, error) {
			return []string{"latest", "nightly", "dev"}, nil
		},
	}

	fetcher := NewDefaultFetcher(mock)

	current := core.ImageUpdate{
		ImageName: "example/repo",
		OldTag:    "1.0.0",
	}

	updated, err := fetcher.FetchUpdate(context.Background(), current)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !updated.NoCompatibleTags {
		t.Error("expected NoCompatibleTags to be true")
	}

	if updated.NewTag != "1.0.0" {
		t.Errorf("expected NewTag to remain 1.0.0, got %s", updated.NewTag)
	}

	t.Logf("Correctly identified no compatible tags")
}

func TestDefaultFetcher_FetchUpdate_VPrefixPreference(t *testing.T) {
	mock := &mockClient{
		getDigestFn: func(ctx context.Context, ref string) (string, error) {
			if ref == "example/repo:v1.0.0" {
				return "sha256:old", nil
			}

			return "sha256:new", nil
		},
		listTagsFn: func(ctx context.Context, repo string) ([]string, error) {
			return []string{"v1.0.0", "v1.1.0", "1.2.0", "v1.3.0"}, nil
		},
	}

	fetcher := NewDefaultFetcher(mock)

	current := core.ImageUpdate{
		ImageName: "example/repo",
		OldTag:    "v1.0.0",
	}

	updated, err := fetcher.FetchUpdate(context.Background(), current)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if updated.NewTag != "v1.3.0" {
		t.Errorf("expected NewTag to be v1.3.0 (v-prefix preference), got %s", updated.NewTag)
	}

	if updated.MajorTag != "" {
		t.Errorf("expected no MajorTag, got %s", updated.MajorTag)
	}

	t.Logf("Correctly preferred v-prefix: %s", updated.NewTag)
}

func TestDefaultFetcher_FetchUpdate_SameMajorPrimary(t *testing.T) {
	mock := &mockClient{
		getDigestFn: func(ctx context.Context, ref string) (string, error) {
			if ref == "example/repo:14.7" {
				return "sha256:old", nil
			}

			return "sha256:new", nil
		},
		listTagsFn: func(ctx context.Context, repo string) ([]string, error) {
			return []string{"14.7", "14.20", "15.0", "18.6"}, nil
		},
	}

	fetcher := NewDefaultFetcher(mock)

	current := core.ImageUpdate{
		ImageName: "example/repo",
		OldTag:    "14.7",
	}

	updated, err := fetcher.FetchUpdate(context.Background(), current)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if updated.NewTag != "14.20" {
		t.Errorf("expected NewTag to be 14.20 (same-major primary), got %s", updated.NewTag)
	}

	if updated.UpdateType != core.UpdateTypeMinor {
		t.Errorf("expected UpdateType to be minor, got %s", updated.UpdateType)
	}

	if !updated.Selected {
		t.Error("expected Selected to be true")
	}

	if updated.MajorTag != "18.6" {
		t.Errorf("expected MajorTag to be 18.6, got %s", updated.MajorTag)
	}

	t.Logf("Same-major primary: %s, major available: %s", updated.NewTag, updated.MajorTag)
}

func TestDefaultFetcher_FetchUpdate_CalVerBoundary(t *testing.T) {
	mock := &mockClient{
		getDigestFn: func(ctx context.Context, ref string) (string, error) {
			if ref == "example/jellyfin:10.8.9" {
				return "sha256:old", nil
			}

			return "sha256:new", nil
		},
		listTagsFn: func(ctx context.Context, repo string) ([]string, error) {
			return []string{"10.8.9", "10.11.11", "2021.12.16"}, nil
		},
	}

	fetcher := NewDefaultFetcher(mock)

	current := core.ImageUpdate{
		ImageName: "example/jellyfin",
		OldTag:    "10.8.9",
	}

	updated, err := fetcher.FetchUpdate(context.Background(), current)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if updated.NewTag != "10.11.11" {
		t.Errorf("expected NewTag to be 10.11.11 (same schema), got %s", updated.NewTag)
	}

	if updated.MajorTag != "" {
		t.Errorf("expected no MajorTag (cross-schema excluded), got %s", updated.MajorTag)
	}

	t.Logf("CalVer boundary: 10.8.9 -> %s (2021.12.16 excluded)", updated.NewTag)
}

func TestDefaultFetcher_FetchUpdate_PrecisionFallback_Successor(t *testing.T) {
	mock := &mockClient{
		getDigestFn: func(ctx context.Context, ref string) (string, error) {
			if ref == "ubuntu:22.04.2" {
				return "", &transport.Error{StatusCode: http.StatusNotFound}
			}

			return "sha256:new", nil
		},
		listTagsFn: func(ctx context.Context, repo string) ([]string, error) {
			return []string{"22.04", "24.04", "26.10"}, nil
		},
	}

	fetcher := NewDefaultFetcher(mock)

	current := core.ImageUpdate{
		ImageName: "ubuntu",
		OldTag:    "22.04.2",
	}

	updated, err := fetcher.FetchUpdate(context.Background(), current)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !updated.OldTagMissing {
		t.Error("expected OldTagMissing to be true")
	}

	if updated.NewTag != "22.04" {
		t.Errorf("expected NewTag to be 22.04 (successor), got %s", updated.NewTag)
	}

	if !updated.Selected {
		t.Error("expected Selected to be true for successor")
	}

	if updated.MajorTag != "26.10" {
		t.Errorf("expected MajorTag to be 26.10, got %s", updated.MajorTag)
	}

	t.Logf("Successor: 22.04.2 (deleted) -> %s, major %s", updated.NewTag, updated.MajorTag)
}

// A channel tag carries fewer version components than the tags published
// alongside it, so it used to report "Up to date" forever: 22.23-alpine was
// invisible to 22-alpine, because the same-precision filter could never come
// back empty and so its widening branch was dead code.
func TestDefaultFetcher_FetchUpdate_WidensWhenPrecisionYieldsNothingNewer(t *testing.T) {
	testFetchUpdate(t, fetchUpdateTestCase{
		name:           "Channel tag widens to a more precise tag",
		imageName:      "node",
		oldTag:         "22-alpine",
		oldDigestRef:   "node:22-alpine",
		tags:           []string{"20-alpine", "22-alpine", "22.23-alpine", "24-alpine"},
		expectedNewTag: "22.23-alpine",
		expectedType:   core.UpdateTypeMinor,
		expectedMajor:  "24-alpine",
	})
}

// Widening is not restricted to tags the current one is a prefix of, so a
// two-component tag still sees a newer two-component release.
func TestDefaultFetcher_FetchUpdate_WidensPastAMinoryComponent(t *testing.T) {
	testFetchUpdate(t, fetchUpdateTestCase{
		name:           "Two-component tag widens past a minor bump",
		imageName:      "example/repo",
		oldTag:         "8.8",
		oldDigestRef:   "example/repo:8.8",
		tags:           []string{"8.8", "8.10.2", "8.11-slim", "9.0"},
		expectedNewTag: "8.10.2",
		expectedType:   core.UpdateTypeMinor,
		expectedMajor:  "9.0",
	})
}

// Widening must not override the preference for the current precision. The
// preference is what stops a three-component tag from being handed a
// four-component one that sorts higher, so the same-precision set has to win
// whenever it holds anything newer. (Within one precision the newest same-major
// tag wins regardless of minor magnitude, so 1.2.3 does become 1.4.0 when no
// 1.2.4 exists -- that is the long-standing rule, pinned below.)
func TestDefaultFetcher_FetchUpdate_KeepsPrecisionWhenItYieldsNewer(t *testing.T) {
	testFetchUpdate(t, fetchUpdateTestCase{
		name:           "Same precision wins when it offers something newer",
		imageName:      "example/repo",
		oldTag:         "1.2.3",
		oldDigestRef:   "example/repo:1.2.3",
		tags:           []string{"1.2.3", "1.2.4", "1.9.9.9", "2.0.0"},
		expectedNewTag: "1.2.4",
		expectedType:   core.UpdateTypePatch,
		expectedMajor:  "2.0.0",
	})
}

// Within a single precision the newest tag of the major wins, so a minor jump
// is offered when no closer release exists. Widening does not change this; it
// only ever adds candidates the precision filter had excluded.
func TestDefaultFetcher_FetchUpdate_NearestPrecisionOffersMinorJump(t *testing.T) {
	testFetchUpdate(t, fetchUpdateTestCase{
		name:           "Nearest precision still allows a minor jump",
		imageName:      "example/repo",
		oldTag:         "1.2.3",
		oldDigestRef:   "example/repo:1.2.3",
		tags:           []string{"1.2.3", "1.4.0", "1.9.9.9", "2.0.0"},
		expectedNewTag: "1.4.0",
		expectedType:   core.UpdateTypeMinor,
		expectedMajor:  "2.0.0",
	})
}

// No same-precision tag may exist at all, which is the one case the old
// fallback did handle. It must widen rather than claim there is nothing to do.
func TestDefaultFetcher_FetchUpdate_WidensWhenNoSamePrecisionTagExists(t *testing.T) {
	testFetchUpdate(t, fetchUpdateTestCase{
		name:           "Empty same-precision set widens",
		imageName:      "example/repo",
		oldTag:         "1.2.3-alpine",
		oldDigestRef:   "example/repo:1.2.3-alpine",
		tags:           []string{"1.2-alpine", "1.3-alpine", "2.0-alpine"},
		expectedNewTag: "1.3-alpine",
		expectedType:   core.UpdateTypeMinor,
		expectedMajor:  "2.0-alpine",
	})
}

// Widening also surfaces a major release the same-precision set could not see.
// It stays reported-only, never pre-selected, so nothing is applied silently.
func TestDefaultFetcher_FetchUpdate_WidenSurfacesInvisibleMajor(t *testing.T) {
	mock := &mockClient{
		getDigestFn: func(ctx context.Context, ref string) (string, error) {
			return "sha256:digest", nil
		},
		listTagsFn: func(ctx context.Context, repo string) ([]string, error) {
			return []string{"1.2", "2.0.1"}, nil
		},
	}

	fetcher := NewDefaultFetcher(mock)

	current := core.ImageUpdate{
		ImageName: "example/repo",
		OldTag:    "1.2",
	}

	updated, err := fetcher.FetchUpdate(context.Background(), current)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if updated.NewTag != "1.2" {
		t.Errorf("expected NewTag to remain 1.2, got %s", updated.NewTag)
	}

	if updated.Selected {
		t.Error("expected Selected to be false: 1.2 is still the newest in its major")
	}

	if updated.MajorTag != "2.0.1" {
		t.Errorf("expected MajorTag to be 2.0.1, got %s", updated.MajorTag)
	}

	t.Logf("Widening surfaced major %s without selecting it", updated.MajorTag)
}

// NoCompatibleTags means no tag shares the base suffix at all. A missing
// same-precision tag is not a reason to claim the image has no candidates.
func TestDefaultFetcher_FetchUpdate_WidenKeepsFlavorIsolation(t *testing.T) {
	mock := &mockClient{
		getDigestFn: func(ctx context.Context, ref string) (string, error) {
			return "sha256:digest", nil
		},
		listTagsFn: func(ctx context.Context, repo string) ([]string, error) {
			return []string{"1.2-alpine", "1.3-alpine", "1.9-bookworm", "2.0"}, nil
		},
	}

	fetcher := NewDefaultFetcher(mock)

	current := core.ImageUpdate{
		ImageName: "example/repo",
		OldTag:    "1.2-alpine",
	}

	updated, err := fetcher.FetchUpdate(context.Background(), current)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if updated.NoCompatibleTags {
		t.Error("expected NoCompatibleTags to be false: alpine candidates do exist")
	}

	if updated.NewTag != "1.3-alpine" {
		t.Errorf("expected NewTag to be 1.3-alpine, got %s", updated.NewTag)
	}

	if updated.MajorTag != "" {
		t.Errorf("expected no MajorTag: 1.9-bookworm is a flavor change, got %s", updated.MajorTag)
	}

	t.Logf("Widening stayed inside the flavor: -> %s", updated.NewTag)
}

func TestDefaultFetcher_FetchUpdate_NonSemver_MissingTag(t *testing.T) {
	mock := &mockClient{
		getDigestFn: func(ctx context.Context, ref string) (string, error) {
			return "", &transport.Error{StatusCode: http.StatusNotFound}
		},
		listTagsFn: func(ctx context.Context, repo string) ([]string, error) {
			// Non-semver tags (plex-style 4-component versions) are filtered out
			return []string{"1.31.1.6733-bc06770e6-ls89", "1.40.0.1234-abc123-ls100"}, nil
		},
	}

	fetcher := NewDefaultFetcher(mock)

	current := core.ImageUpdate{
		ImageName: "linuxserver/plex",
		OldTag:    "1.31.1.6733-bc06770e6-ls89",
	}

	updated, err := fetcher.FetchUpdate(context.Background(), current)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !updated.OldTagMissing {
		t.Error("expected OldTagMissing to be true")
	}

	// Non-semver tags are filtered out, so NoCompatibleTags is true
	if !updated.NoCompatibleTags {
		t.Error("expected NoCompatibleTags to be true (non-semver tags filtered)")
	}

	t.Logf("Non-semver missing tag: best-effort ListTags succeeded, NoCompatibleTags=true")
}

func TestDefaultFetcher_FetchUpdate_JEP223(t *testing.T) {
	testFetchUpdate(t, fetchUpdateTestCase{
		name:           "JEP-223",
		imageName:      "eclipse-temurin",
		oldTag:         "17.0.6_10-jre",
		oldDigestRef:   "eclipse-temurin:17.0.6_10-jre",
		tags:           []string{"17.0.6_10-jre", "17.0.12_8-jre", "17.0.16_7-jre", "21.0.2_13-jre"},
		expectedNewTag: "17.0.16_7-jre",
		expectedMajor:  "21.0.2_13-jre",
	})
}

func TestDefaultFetcher_FetchUpdate_LinuxServer_BuildID(t *testing.T) {
	testFetchUpdate(t, fetchUpdateTestCase{
		name:           "LinuxServer BuildID",
		imageName:      "linuxserver/nextcloud",
		oldTag:         "25.0.4-ls212",
		oldDigestRef:   "linuxserver/nextcloud:25.0.4-ls212",
		tags:           []string{"25.0.4-ls212", "25.0.5-ls215", "25.0.13-ls260", "26.0.0-ls100"},
		expectedNewTag: "25.0.13-ls260",
		expectedMajor:  "26.0.0-ls100",
	})
}

type fetchUpdateTestCase struct {
	name           string
	imageName      string
	oldTag         string
	oldDigestRef   string
	tags           []string
	expectedNewTag string
	expectedType   core.UpdateType
	expectedMajor  string
}

func testFetchUpdate(t *testing.T, tc fetchUpdateTestCase) {
	t.Helper()

	mock := &mockClient{
		getDigestFn: func(ctx context.Context, ref string) (string, error) {
			if ref == tc.oldDigestRef {
				return "sha256:old", nil
			}

			return "sha256:new", nil
		},
		listTagsFn: func(ctx context.Context, repo string) ([]string, error) {
			return tc.tags, nil
		},
	}

	fetcher := NewDefaultFetcher(mock)

	current := core.ImageUpdate{
		ImageName: tc.imageName,
		OldTag:    tc.oldTag,
	}

	updated, err := fetcher.FetchUpdate(context.Background(), current)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if updated.NewTag != tc.expectedNewTag {
		t.Errorf("expected NewTag to be %s, got %s", tc.expectedNewTag, updated.NewTag)
	}

	if !updated.Selected {
		t.Error("expected Selected to be true")
	}

	if tc.expectedType != "" && updated.UpdateType != tc.expectedType {
		t.Errorf("expected UpdateType to be %s, got %s", tc.expectedType, updated.UpdateType)
	}

	if updated.MajorTag != tc.expectedMajor {
		t.Errorf("expected MajorTag to be %s, got %s", tc.expectedMajor, updated.MajorTag)
	}

	t.Logf("%s: %s -> %s, major %s", tc.name, tc.oldTag, updated.NewTag, updated.MajorTag)
}
