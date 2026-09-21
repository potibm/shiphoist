// Package registry provides functionality for interacting with container registries.
package registry

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	cacheDirPermissions  = 0o755
	cacheFilePermissions = 0o600
)

// CachedClient dekoriert einen RegistryClient mit einem Datei-basierten Cache.
type CachedClient struct {
	upstream     RegistryClient
	cacheDir     string
	ttl          time.Duration
	forceRefresh bool
}

type cacheEntry struct {
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data"` // Flexible für []string oder string
}

func NewCachedClient(upstream RegistryClient, ttl time.Duration, forceRefresh bool) (*CachedClient, error) {
	userCache, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}

	cacheDir := filepath.Join(userCache, "shiphoist")
	if err := os.MkdirAll(cacheDir, cacheDirPermissions); err != nil {
		return nil, err
	}

	return &CachedClient{
		upstream:     upstream,
		cacheDir:     cacheDir,
		ttl:          ttl,
		forceRefresh: forceRefresh,
	}, nil
}

func (c *CachedClient) ListTags(ctx context.Context, repo string) ([]string, error) {
	cacheKey := "tags-" + repo

	if !c.forceRefresh {
		if data, ok := c.read(cacheKey); ok {
			var tags []string
			if err := json.Unmarshal(data, &tags); err == nil {
				return tags, nil
			}
		}
	}

	tags, err := c.upstream.ListTags(ctx, repo)
	if err != nil {
		return nil, err
	}

	if raw, err := json.Marshal(tags); err == nil {
		c.write(cacheKey, raw)
	}

	return tags, nil
}

func (c *CachedClient) GetDigest(ctx context.Context, ref string) (string, error) {
	cacheKey := "digest-" + ref

	if !c.forceRefresh {
		if data, ok := c.read(cacheKey); ok {
			var digest string
			if err := json.Unmarshal(data, &digest); err == nil {
				return digest, nil
			}
		}
	}

	digest, err := c.upstream.GetDigest(ctx, ref)
	if err != nil {
		return "", err
	}

	if raw, err := json.Marshal(digest); err == nil {
		c.write(cacheKey, raw)
	}

	return digest, nil
}

// --- Hilfsmethoden für I/O ---.
func (c *CachedClient) cachePath(key string) string {
	hash := sha256.Sum256([]byte(key))

	return filepath.Join(c.cacheDir, fmt.Sprintf("%x.json", hash))
}

func (c *CachedClient) read(key string) (json.RawMessage, bool) {
	data, err := os.ReadFile(c.cachePath(key))
	if err != nil {
		return nil, false
	}

	var entry cacheEntry
	if err := json.Unmarshal(data, &entry); err != nil || time.Since(entry.Timestamp) > c.ttl {
		return nil, false
	}

	return entry.Data, true
}

func (c *CachedClient) write(key string, data json.RawMessage) {
	entry := cacheEntry{Timestamp: time.Now(), Data: data}
	if b, err := json.Marshal(entry); err == nil {
		_ = os.WriteFile(c.cachePath(key), b, cacheFilePermissions)
	}
}
