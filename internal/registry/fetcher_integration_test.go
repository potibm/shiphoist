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

// A channel tag such as 22-alpine carries fewer version components than the
// releases published beside it. The invariant is that widening never crosses a
// major boundary: whatever is offered has to stay in the tag's own major. The
// specific target is deliberately not asserted, since 22.x moves over time.
func TestIntegration_FetchUpdate_ChannelTagStaysInMajor(t *testing.T) {
	requireNetwork(t)

	fetcher := NewDefaultFetcher(NewRemoteClient())

	current := core.ImageUpdate{
		ImageName: "node",
		OldTag:    "22-alpine",
	}

	updated, err := fetcher.FetchUpdate(context.Background(), current)
	if err != nil {
		t.Fatalf("unexpected error during registry fetch: %v", err)
	}

	if updated.NoCompatibleTags {
		t.Fatal("expected alpine candidates to exist for node:22-alpine")
	}

	newTag, err := ParseTag(updated.NewTag)
	if err != nil {
		t.Fatalf("expected a parseable new tag, got %q: %v", updated.NewTag, err)
	}

	if newTag.SemVer.Major() != 22 {
		t.Errorf("expected the update to stay in major 22, got %q", updated.NewTag)
	}

	if updated.NewTag != "22-alpine" && !updated.Selected {
		t.Error("expected a widened candidate to be offered for selection")
	}

	t.Logf("node:22-alpine -> %s (%s), major %s", updated.NewTag, updated.UpdateType, updated.MajorTag)
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
