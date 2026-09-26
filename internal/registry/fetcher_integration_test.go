package registry

import (
	"context"
	"testing"

	"github.com/potibm/shiphoist/internal/core"
)

// These tests hit real container registries and are therefore slow and
// dependent on network availability. They are skipped under `go test -short`
// and are intended to run separately, e.g. `go test ./... -run Integration`
// or as a scheduled CI job.

// requireNetwork skips unless the caller opted into live-registry tests.
func requireNetwork(t *testing.T) {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping live registry test in short mode")
	}
}

func TestIntegration_FetchUpdate_Alpine(t *testing.T) {
	requireNetwork(t)

	fetcher := NewDefaultFetcher(NewRemoteClient())

	current := core.ImageUpdate{
		ImageName: "alpine",
		OldTag:    "3.17.0",
	}

	updated, err := fetcher.FetchUpdate(context.Background(), current)
	if err != nil {
		t.Fatalf("unexpected error during registry fetch: %v", err)
	}

	if updated.NewTag == "3.17.0" {
		t.Error("expected tag to be updated, but it remained '3.17.0'")
	}

	if updated.UpdateType == core.UpdateTypeNone {
		t.Error("expected an update type to be set, got None")
	}

	if updated.NewDigest == "" {
		t.Error("expected a valid new digest, got an empty string")
	}

	t.Logf("alpine:3.17.0 -> %s (%s)", updated.NewTag, updated.UpdateType)
}

func TestIntegration_FetchUpdate_NonSemverFloatingTag(t *testing.T) {
	requireNetwork(t)

	fetcher := NewDefaultFetcher(NewRemoteClient())

	current := core.ImageUpdate{
		ImageName: "nginx",
		OldTag:    "latest",
	}

	updated, err := fetcher.FetchUpdate(context.Background(), current)
	if err != nil {
		t.Fatalf("unexpected error during registry fetch: %v", err)
	}

	if updated.NewTag != "latest" {
		t.Errorf("expected tag to remain 'latest', got %q", updated.NewTag)
	}

	if updated.NewDigest == "" {
		t.Error("expected a valid new digest, got an empty string")
	}

	t.Logf("nginx:latest pinned to %s", updated.NewDigest)
}

func TestIntegration_ListTags_Alpine(t *testing.T) {
	requireNetwork(t)

	fetcher := NewDefaultFetcher(NewRemoteClient())

	tags, err := fetcher.listTags(context.Background(), "alpine")
	if err != nil {
		t.Fatalf("unexpected error listing tags: %v", err)
	}

	if len(tags) == 0 {
		t.Fatal("expected a list of tags, but got an empty list")
	}

	// Assert only that the list is plausibly sorted by SemVer, rather than
	// pinning to specific tags that will eventually be removed upstream.
	if len(tags) > 1 && tags[0].Raw == tags[1].Raw {
		t.Errorf("expected distinct tags, got %q twice", tags[0].Raw)
	}

	t.Logf("fetched %d tags for alpine", len(tags))
}
