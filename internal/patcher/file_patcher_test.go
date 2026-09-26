package patcher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/potibm/shiphoist/internal/core"
)

const patchFilePermissions = 0o600

// writeTempFile creates a file with restrictive permissions, matching how the
// patcher expects to find its input.
func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), patchFilePermissions); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s: %v", path, err)
	}

	return string(data)
}

func TestFilePatcher_Patch(t *testing.T) {
	originalYAML := `version: '3.8'

services:
  web:
    # This comment must survive
    image: nginx:1.25.0-alpine
    ports:
      - "80:80"
  db:
    image: postgres:15-bullseye # Inline comment here
    environment:
      - POSTGRES_PASSWORD=secret`

	expectedYAML := `version: '3.8'

services:
  web:
    # This comment must survive
    image: nginx:1.25.0-alpine@sha256:1234567890abcdef
    ports:
      - "80:80"
  db:
    image: postgres:15-bullseye@sha256:fedcba0987654321 # Inline comment here
    environment:
      - POSTGRES_PASSWORD=secret`

	path := writeTempFile(t, "docker-compose.yaml", originalYAML)

	updates := []core.ImageUpdate{
		{
			LineNumber:     6, // Line with nginx
			OriginalString: "nginx:1.25.0-alpine",
			ImageName:      "nginx",
			NewTag:         "1.25.0-alpine",
			NewDigest:      "sha256:1234567890abcdef",
		},
		{
			LineNumber:     10, // Line with postgres
			OriginalString: "postgres:15-bullseye",
			ImageName:      "postgres",
			NewTag:         "15-bullseye",
			NewDigest:      "sha256:fedcba0987654321",
		},
	}

	patcher := &FilePatcher{}

	if err := patcher.Patch(context.Background(), path, updates); err != nil {
		t.Fatalf("unexpected error during Patch: %v", err)
	}

	if got, want := strings.TrimSpace(readFile(t, path)), strings.TrimSpace(expectedYAML); got != want {
		t.Errorf("patched YAML does not match expected.\n\nExpected:\n%s\n\nGot:\n%s", want, got)
	}
}

// Only the first occurrence on a line is replaced, so a line mentioning the
// same image twice is only partially rewritten rather than mangled.
func TestFilePatcher_Patch_ReplacesOnlyFirstOccurrenceOnLine(t *testing.T) {
	const content = `image: nginx:1.25.0-alpine # was nginx:1.25.0-alpine`

	path := writeTempFile(t, "compose.yaml", content)

	updates := []core.ImageUpdate{{
		LineNumber:     1,
		OriginalString: "nginx:1.25.0-alpine",
		ImageName:      "nginx",
		NewTag:         "1.25.0-alpine",
		NewDigest:      "sha256:aaa",
	}}

	if err := (&FilePatcher{}).Patch(context.Background(), path, updates); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "image: nginx:1.25.0-alpine@sha256:aaa # was nginx:1.25.0-alpine"
	if got := strings.TrimSpace(readFile(t, path)); got != want {
		t.Errorf("expected only the first occurrence to change.\nExpected: %s\nGot:      %s", want, got)
	}
}

func TestFilePatcher_Patch_RepinsAlreadyPinnedImage(t *testing.T) {
	const oldDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

	content := "image: nginx:1.25.0-alpine@" + oldDigest

	path := writeTempFile(t, "compose.yaml", content)

	updates := []core.ImageUpdate{{
		LineNumber:     1,
		OriginalString: "nginx:1.25.0-alpine@" + oldDigest,
		ImageName:      "nginx",
		NewTag:         "1.26.0-alpine",
		NewDigest:      "sha256:2222222222222222222222222222222222222222222222222222222222222222",
	}}

	if err := (&FilePatcher{}).Patch(context.Background(), path, updates); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := strings.TrimSpace(readFile(t, path))
	if strings.Contains(got, oldDigest) {
		t.Errorf("expected the stale digest to be replaced, got %s", got)
	}

	want := "image: nginx:1.26.0-alpine@sha256:2222222222222222222222222222222222222222222222222222222222222222"
	if got != want {
		t.Errorf("expected %s, got %s", want, got)
	}
}

// An empty digest means the registry never resolved one; writing a reference
// without it would silently drop the pinning, so the line must be left alone.
func TestFilePatcher_Patch_SkipsEmptyDigest(t *testing.T) {
	const content = "image: nginx:1.25.0-alpine"

	path := writeTempFile(t, "compose.yaml", content)

	updates := []core.ImageUpdate{{
		LineNumber:     1,
		OriginalString: "nginx:1.25.0-alpine",
		ImageName:      "nginx",
		NewTag:         "1.26.0-alpine",
		NewDigest:      "",
	}}

	if err := (&FilePatcher{}).Patch(context.Background(), path, updates); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := strings.TrimSpace(readFile(t, path)); got != content {
		t.Errorf("expected the line to be untouched, got %s", got)
	}
}

func TestFilePatcher_Patch_EmptyUpdatesIsNoop(t *testing.T) {
	const content = "image: nginx:1.25.0-alpine"

	path := writeTempFile(t, "compose.yaml", content)

	if err := (&FilePatcher{}).Patch(context.Background(), path, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := strings.TrimSpace(readFile(t, path)); got != content {
		t.Errorf("expected no change, got %s", got)
	}
}

func TestFilePatcher_Patch_Errors(t *testing.T) {
	const content = "image: nginx:1.25.0-alpine\nimage: redis:7.2"

	tests := []struct {
		name        string
		fileName    string
		missingFile bool
		updates     []core.ImageUpdate
		expectedErr string
	}{
		{
			name:     "line number beyond end of file",
			fileName: "compose.yaml",
			updates: []core.ImageUpdate{{
				LineNumber:     99,
				OriginalString: "nginx:1.25.0-alpine",
				ImageName:      "nginx",
				NewTag:         "1.26.0-alpine",
				NewDigest:      "sha256:aaa",
			}},
			expectedErr: "out of range",
		},
		{
			name:     "zero line number",
			fileName: "compose.yaml",
			updates: []core.ImageUpdate{{
				LineNumber:     0,
				OriginalString: "nginx:1.25.0-alpine",
				ImageName:      "nginx",
				NewTag:         "1.26.0-alpine",
				NewDigest:      "sha256:aaa",
			}},
			expectedErr: "out of range",
		},
		{
			name:     "negative line number",
			fileName: "compose.yaml",
			updates: []core.ImageUpdate{{
				LineNumber:     -3,
				OriginalString: "nginx:1.25.0-alpine",
				ImageName:      "nginx",
				NewTag:         "1.26.0-alpine",
				NewDigest:      "sha256:aaa",
			}},
			expectedErr: "out of range",
		},
		{
			name:        "file does not exist",
			fileName:    "missing.yaml",
			missingFile: true,
			updates:     nil,
			expectedErr: "failed to open file",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tc.fileName)
			if !tc.missingFile {
				if err := os.WriteFile(path, []byte(content), patchFilePermissions); err != nil {
					t.Fatalf("failed to write temp file: %v", err)
				}
			}

			err := (&FilePatcher{}).Patch(context.Background(), path, tc.updates)
			if err == nil {
				t.Fatalf("expected an error containing %q, got nil", tc.expectedErr)
			}

			if !strings.Contains(err.Error(), tc.expectedErr) {
				t.Errorf("expected an error containing %q, got %v", tc.expectedErr, err)
			}
		})
	}
}

// An out-of-range line must abort before the file is rewritten, so a bad
// update set cannot leave the file half-patched.
func TestFilePatcher_Patch_AbortsBeforeWritingOnBadLine(t *testing.T) {
	const content = "image: nginx:1.25.0-alpine\nimage: redis:7.2"

	path := writeTempFile(t, "compose.yaml", content)

	updates := []core.ImageUpdate{
		{
			LineNumber:     1,
			OriginalString: "nginx:1.25.0-alpine",
			ImageName:      "nginx",
			NewTag:         "1.26.0-alpine",
			NewDigest:      "sha256:aaa",
		},
		{
			LineNumber:     99,
			OriginalString: "redis:7.2",
			ImageName:      "redis",
			NewTag:         "7.3",
			NewDigest:      "sha256:bbb",
		},
	}

	if err := (&FilePatcher{}).Patch(context.Background(), path, updates); err == nil {
		t.Fatal("expected an error")
	}

	if got := readFile(t, path); got != content {
		t.Errorf("expected the file to be left untouched, got:\n%s", got)
	}
}

// The patcher rewrites the file, so it must not widen its permissions.
func TestFilePatcher_Patch_KeepsRestrictivePermissions(t *testing.T) {
	path := writeTempFile(t, "compose.yaml", "image: nginx:1.25.0-alpine")

	updates := []core.ImageUpdate{{
		LineNumber:     1,
		OriginalString: "nginx:1.25.0-alpine",
		ImageName:      "nginx",
		NewTag:         "1.26.0-alpine",
		NewDigest:      "sha256:aaa",
	}}

	if err := (&FilePatcher{}).Patch(context.Background(), path, updates); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("failed to stat %s: %v", path, err)
	}

	if perm := info.Mode().Perm(); perm != filePermissions {
		t.Errorf("expected permissions %o, got %o", filePermissions, perm)
	}
}

// Two services can share a repository; patching one must not touch the other.
func TestFilePatcher_Patch_SameRepoOnSeparateLines(t *testing.T) {
	const content = `services:
  db:
    image: postgres:16
  cache:
    image: postgres:16-alpine`

	path := writeTempFile(t, "compose.yaml", content)

	updates := []core.ImageUpdate{{
		LineNumber:     5, // the cache service's image line, not the db one
		ServiceName:    "cache",
		OriginalString: "postgres:16-alpine",
		ImageName:      "postgres",
		NewTag:         "16.4-alpine",
		NewDigest:      "sha256:aaa",
	}}

	if err := (&FilePatcher{}).Patch(context.Background(), path, updates); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := `services:
  db:
    image: postgres:16
  cache:
    image: postgres:16.4-alpine@sha256:aaa`

	if got := strings.TrimSpace(readFile(t, path)); got != want {
		t.Errorf("expected only the alpine line to change.\nExpected:\n%s\nGot:\n%s", want, got)
	}
}
