package patcher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/potibm/shiphoist/internal/core"
)

func TestFilePatcher_Patch(t *testing.T) {
	// Original YAML with a mix of formatting and comments
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

	// Expected YAML after patching
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

	tempDir := t.TempDir()
	tempFile := filepath.Join(tempDir, "docker-compose.yaml")

	err := os.WriteFile(tempFile, []byte(originalYAML), 0o600)
	if err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

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

	err = patcher.Patch(context.Background(), tempFile, updates)
	if err != nil {
		t.Fatalf("unexpected error during Patch: %v", err)
	}

	patchedData, err := os.ReadFile(tempFile)
	if err != nil {
		t.Fatalf("failed to read patched file: %v", err)
	}

	patchedStr := strings.TrimSpace(string(patchedData))
	expectedStr := strings.TrimSpace(expectedYAML)

	if patchedStr != expectedStr {
		t.Errorf("Patched YAML does not match expected YAML.\n\nExpected:\n%s\n\nGot:\n%s", expectedStr, patchedStr)
	}
}
