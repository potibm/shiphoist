package registry

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// countingClient records how often the upstream is called so cache hits and
// misses can be asserted without inspecting files.
type countingClient struct {
	tags   []string
	digest string

	err   error
	calls atomic.Int32
}

func (c *countingClient) GetDigest(ctx context.Context, ref string) (string, error) {
	c.calls.Add(1)

	return c.digest, c.err
}

func (c *countingClient) ListTags(ctx context.Context, repo string) ([]string, error) {
	c.calls.Add(1)

	return c.tags, c.err
}

func newTestCache(t *testing.T, upstream RegistryClient, ttl time.Duration, forceRefresh bool) *CachedClient {
	t.Helper()

	cached, err := NewCachedClientInDir(upstream, filepath.Join(t.TempDir(), "cache"), ttl, forceRefresh)
	if err != nil {
		t.Fatalf("failed to create cache: %v", err)
	}

	return cached
}

func TestCachedClient_ListTags_CachesSecondCall(t *testing.T) {
	upstream := &countingClient{tags: []string{"1.0.0", "1.0.1"}}
	cached := newTestCache(t, upstream, time.Hour, false)

	first, err := cached.ListTags(context.Background(), "alpine")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	second, err := cached.ListTags(context.Background(), "alpine")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("expected 2 tags on both calls, got %d and %d", len(first), len(second))
	}

	if calls := upstream.calls.Load(); calls != 1 {
		t.Errorf("expected upstream to be called once, got %d", calls)
	}
}

func TestCachedClient_GetDigest_CachesSecondCall(t *testing.T) {
	upstream := &countingClient{digest: "sha256:abc"}
	cached := newTestCache(t, upstream, time.Hour, false)

	for range 3 {
		got, err := cached.GetDigest(context.Background(), "alpine:1.0.0")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if got != "sha256:abc" {
			t.Errorf("expected sha256:abc, got %q", got)
		}
	}

	if calls := upstream.calls.Load(); calls != 1 {
		t.Errorf("expected upstream to be called once, got %d", calls)
	}
}

// Different repos must not collide, otherwise one image would serve another's
// tag list.
func TestCachedClient_KeysAreScopedPerRef(t *testing.T) {
	upstream := &countingClient{tags: []string{"1.0.0"}}
	cached := newTestCache(t, upstream, time.Hour, false)

	if _, err := cached.ListTags(context.Background(), "alpine"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := cached.ListTags(context.Background(), "nginx"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if calls := upstream.calls.Load(); calls != 2 {
		t.Errorf("expected two distinct upstream calls, got %d", calls)
	}
}

func TestCachedClient_ForceRefreshBypassesCache(t *testing.T) {
	upstream := &countingClient{tags: []string{"1.0.0"}}
	cached := newTestCache(t, upstream, time.Hour, true)

	for range 2 {
		if _, err := cached.ListTags(context.Background(), "alpine"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if calls := upstream.calls.Load(); calls != 2 {
		t.Errorf("expected forceRefresh to hit upstream twice, got %d", calls)
	}
}

func TestCachedClient_ExpiredEntryIsRefetched(t *testing.T) {
	upstream := &countingClient{digest: "sha256:abc"}
	cached := newTestCache(t, upstream, 0, false)

	if _, err := cached.GetDigest(context.Background(), "alpine:1.0.0"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// A zero TTL means every entry is already stale.
	if _, err := cached.GetDigest(context.Background(), "alpine:1.0.0"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if calls := upstream.calls.Load(); calls != 2 {
		t.Errorf("expected expired entry to be refetched, got %d upstream calls", calls)
	}
}

func TestCachedClient_UpstreamErrorIsNotCached(t *testing.T) {
	upstream := &countingClient{err: errors.New("registry down")}
	cached := newTestCache(t, upstream, time.Hour, false)

	if _, err := cached.GetDigest(context.Background(), "alpine:1.0.0"); err == nil {
		t.Fatal("expected an error from the upstream call")
	}

	// The second call must reach upstream again rather than replay a
	// poisoned or empty entry.
	if _, err := cached.GetDigest(context.Background(), "alpine:1.0.0"); err == nil {
		t.Fatal("expected an error from the second upstream call")
	}

	if calls := upstream.calls.Load(); calls != 2 {
		t.Errorf("expected errors not to be cached, got %d upstream calls", calls)
	}
}

// A truncated or hand-edited cache file must degrade to a refetch rather than
// return garbage.
func TestCachedClient_CorruptEntryFallsBackToUpstream(t *testing.T) {
	upstream := &countingClient{tags: []string{"1.0.0"}}
	cached := newTestCache(t, upstream, time.Hour, false)

	if _, err := cached.ListTags(context.Background(), "alpine"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	corruptAllCacheFiles(t, cached.cacheDir, []byte("{not json"))

	got, err := cached.ListTags(context.Background(), "alpine")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got) != 1 {
		t.Errorf("expected 1 tag from upstream, got %d", len(got))
	}

	if calls := upstream.calls.Load(); calls != 2 {
		t.Errorf("expected a refetch after corruption, got %d upstream calls", calls)
	}
}

// Valid JSON with the wrong shape must also be rejected.
func TestCachedClient_MalformedEntryFallsBackToUpstream(t *testing.T) {
	upstream := &countingClient{digest: "sha256:abc"}
	cached := newTestCache(t, upstream, time.Hour, false)

	if _, err := cached.GetDigest(context.Background(), "alpine:1.0.0"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// `data` holds a JSON array where a string is required.
	writeEntry(t, cached, "digest-alpine:1.0.0", json.RawMessage(`["sha256:not-a-string"]`))

	got, err := cached.GetDigest(context.Background(), "alpine:1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got != "sha256:abc" {
		t.Errorf("expected a refetch from upstream, got %q", got)
	}

	if calls := upstream.calls.Load(); calls != 2 {
		t.Errorf("expected a refetch after malformed data, got %d upstream calls", calls)
	}
}

func TestCachedClient_CacheDirIsCreated(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "cache")

	if _, err := NewCachedClientInDir(&countingClient{}, dir, time.Hour, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("expected the cache directory to exist: %v", err)
	}

	if !info.IsDir() {
		t.Errorf("expected %s to be a directory", dir)
	}
}

func TestNewCachedClient_UsesUserCacheDir(t *testing.T) {
	userCache, err := os.UserCacheDir()
	if err != nil {
		t.Skipf("no user cache dir available: %v", err)
	}

	cached, err := NewCachedClient(&countingClient{}, time.Hour, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := filepath.Join(userCache, "shiphoist")
	if cached.cacheDir != expected {
		t.Errorf("expected cache dir %q, got %q", expected, cached.cacheDir)
	}
}

// The cache is a shared, user-writable directory, so entries must not be
// readable by other users.
func TestCachedClient_EntryFilePermissions(t *testing.T) {
	upstream := &countingClient{digest: "sha256:abc"}
	cached := newTestCache(t, upstream, time.Hour, false)

	if _, err := cached.GetDigest(context.Background(), "alpine:1.0.0"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := os.Stat(cached.cachePath("digest-alpine:1.0.0"))
	if err != nil {
		t.Fatalf("expected a cache file to exist: %v", err)
	}

	if perm := info.Mode().Perm(); perm != cacheFilePermissions {
		t.Errorf("expected permissions %o, got %o", cacheFilePermissions, perm)
	}
}

func TestCachedClient_CachePathIsStableAndUnique(t *testing.T) {
	cached := newTestCache(t, &countingClient{}, time.Hour, false)

	if got, want := cached.cachePath("a"), cached.cachePath("a"); got != want {
		t.Errorf("expected a stable path, got %q and %q", got, want)
	}

	if cached.cachePath("a") == cached.cachePath("b") {
		t.Error("expected different keys to map to different paths")
	}
}

func writeEntry(t *testing.T, c *CachedClient, key string, data json.RawMessage) {
	t.Helper()

	entry := cacheEntry{Timestamp: time.Now(), Data: data}

	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("failed to marshal cache entry: %v", err)
	}

	if err := os.WriteFile(c.cachePath(key), raw, cacheFilePermissions); err != nil {
		t.Fatalf("failed to write cache entry: %v", err)
	}
}

func corruptAllCacheFiles(t *testing.T, dir string, contents []byte) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("failed to read cache dir: %v", err)
	}

	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if err := os.WriteFile(path, contents, cacheFilePermissions); err != nil {
			t.Fatalf("failed to corrupt %s: %v", path, err)
		}
	}
}
