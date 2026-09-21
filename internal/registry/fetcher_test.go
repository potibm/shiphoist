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

func TestDefaultFetcher_FetchUpdate(t *testing.T) {
	fetcher := NewDefaultFetcher(NewRemoteClient())

	// Start with an intentionally old Alpine tag
	current := core.ImageUpdate{
		ImageName: "alpine",
		OldTag:    "3.17.0",
		OldDigest: "",
	}

	updated, err := fetcher.FetchUpdate(context.Background(), current)
	if err != nil {
		t.Fatalf("unexpected error during registry fetch: %v", err)
	}

	// We expect the tool to find a newer version (e.g., 3.17.3 or 3.20.0 depending on the suffix match logic)
	// Since 3.17.0 has no suffix, it will match all other tags without a suffix and find the absolute latest.
	if updated.NewTag == "3.17.0" {
		t.Errorf("expected tag to be updated, but it remained '3.17.0'")
	}

	if updated.UpdateType == core.UpdateTypeNone {
		t.Errorf("expected an update type to be set, got None")
	}

	if updated.NewDigest == "" {
		t.Error("expected a valid new digest, got an empty string")
	}

	t.Logf("Successfully found update for alpine:3.17.0 -> %s (%s)", updated.NewTag, updated.UpdateType)
}

func TestDefaultFetcher_FetchUpdate_NonSemverFallback(t *testing.T) {
	fetcher := NewDefaultFetcher(NewRemoteClient())

	// We use "latest", which is definitively not valid SemVer
	current := core.ImageUpdate{
		ImageName: "nginx",
		OldTag:    "latest",
		OldDigest: "",
	}

	updated, err := fetcher.FetchUpdate(context.Background(), current)
	if err != nil {
		t.Fatalf("unexpected error during registry fetch: %v", err)
	}

	// We expect the tag to remain exactly the same ("latest")
	if updated.NewTag != "latest" {
		t.Errorf("expected tag to remain 'latest', but got '%s'", updated.NewTag)
	}

	// We expect it to have fetched a valid digest for the latest tag
	if updated.NewDigest == "" {
		t.Error("expected a valid new digest, got an empty string")
	}

	t.Logf("Successfully pinned non-semver tag nginx:latest to digest %s", updated.NewDigest)
}

func TestDefaultFetcher_ListTags(t *testing.T) {
	fetcher := NewDefaultFetcher(NewRemoteClient())

	// Call the registry to list tags for the "alpine" image
	tags, err := fetcher.listTags(context.Background(), "alpine")
	if err != nil {
		t.Fatalf("unexpected error listing tags: %v", err)
	}

	// We expect Alpine to have at least some tags
	if len(tags) == 0 {
		t.Fatal("expected a list of tags, but got an empty list")
	}

	// Let's look for a tag we know definitely exists
	found := false
	for _, tag := range tags {
		if tag.Raw == "3.18" {
			found = true
			break
		}
	}

	if !found {
		t.Errorf("expected to find tag '3.18' in the list, but it was missing. Got %d total tags.", len(tags))
	}

	t.Logf("Successfully fetched %d tags for alpine", len(tags))
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
	mock := &mockClient{
		getDigestFn: func(ctx context.Context, ref string) (string, error) {
			if ref == "eclipse-temurin:17.0.6_10-jre" {
				return "sha256:old", nil
			}
			return "sha256:new", nil
		},
		listTagsFn: func(ctx context.Context, repo string) ([]string, error) {
			return []string{"17.0.6_10-jre", "17.0.12_8-jre", "17.0.16_7-jre", "21.0.2_13-jre"}, nil
		},
	}

	fetcher := NewDefaultFetcher(mock)

	current := core.ImageUpdate{
		ImageName: "eclipse-temurin",
		OldTag:    "17.0.6_10-jre",
	}

	updated, err := fetcher.FetchUpdate(context.Background(), current)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if updated.NewTag != "17.0.16_7-jre" {
		t.Errorf("expected NewTag to be 17.0.16_7-jre (same-major JEP-223), got %s", updated.NewTag)
	}

	if !updated.Selected {
		t.Error("expected Selected to be true")
	}

	if updated.MajorTag != "21.0.2_13-jre" {
		t.Errorf("expected MajorTag to be 21.0.2_13-jre, got %s", updated.MajorTag)
	}

	t.Logf("JEP-223: 17.0.6_10-jre -> %s, major %s", updated.NewTag, updated.MajorTag)
}

func TestDefaultFetcher_FetchUpdate_LinuxServer_BuildID(t *testing.T) {
	mock := &mockClient{
		getDigestFn: func(ctx context.Context, ref string) (string, error) {
			if ref == "linuxserver/nextcloud:25.0.4-ls212" {
				return "sha256:old", nil
			}
			return "sha256:new", nil
		},
		listTagsFn: func(ctx context.Context, repo string) ([]string, error) {
			return []string{"25.0.4-ls212", "25.0.5-ls215", "25.0.13-ls260", "26.0.0-ls100"}, nil
		},
	}

	fetcher := NewDefaultFetcher(mock)

	current := core.ImageUpdate{
		ImageName: "linuxserver/nextcloud",
		OldTag:    "25.0.4-ls212",
	}

	updated, err := fetcher.FetchUpdate(context.Background(), current)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if updated.NewTag != "25.0.13-ls260" {
		t.Errorf("expected NewTag to be 25.0.13-ls260 (highest build ID in same-major), got %s", updated.NewTag)
	}

	if !updated.Selected {
		t.Error("expected Selected to be true")
	}

	if updated.MajorTag != "26.0.0-ls100" {
		t.Errorf("expected MajorTag to be 26.0.0-ls100, got %s", updated.MajorTag)
	}

	t.Logf("LinuxServer BuildID: 25.0.4-ls212 -> %s, major %s", updated.NewTag, updated.MajorTag)
}
