package discovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/potibm/shiphoist/internal/core"
)

func TestComposeDiscoverer_Discover(t *testing.T) {
	yamlContent := `version: '3.8'

services:
  web:
    build: .
    # A nasty comment right before the image
    image: nginx:1.25.0-alpine
    ports:
      - "80:80"

  db:
    image: postgres:15-bullseye
    environment:
      - POSTGRES_PASSWORD=secret

  redis:
    restart: always
    # Another comment
    # spanning two lines
    image: redis:7.2

  worker:
    # An already pinned image
    image: node:20-slim@sha256:7b55dbb5c2bf96cc9a7213bb1b6be91176bcab07a523a1aeb0cf0c52402120e8`

	tempDir := t.TempDir()
	tempFile := filepath.Join(tempDir, "docker-compose.yaml")
	err := os.WriteFile(tempFile, []byte(yamlContent), 0644)
	if err != nil {
		t.Fatalf("could not write temp file: %v", err)
	}

	discoverer := &ComposeDiscoverer{}
	updates, err := discoverer.Discover(context.Background(), tempFile)

	if err != nil {
		t.Fatalf("unexpected error during Discover: %v", err)
	}

	if len(updates) != 4 {
		t.Fatalf("expected 4 updates, got %d", len(updates))
	}

	expected := []core.ImageUpdate{
		{
			LineNumber:     7,
			ImageName:      "nginx",
			OldTag:         "1.25.0-alpine",
			OldDigest:      "",
			OriginalString: "nginx:1.25.0-alpine",
		},
		{
			LineNumber:     12,
			ImageName:      "postgres",
			OldTag:         "15-bullseye",
			OldDigest:      "",
			OriginalString: "postgres:15-bullseye",
		},
		{
			LineNumber:     20,
			ImageName:      "redis",
			OldTag:         "7.2",
			OldDigest:      "",
			OriginalString: "redis:7.2",
		},
		{
			LineNumber:     24,
			ImageName:      "node",
			OldTag:         "20-slim",
			OldDigest:      "sha256:7b55dbb5c2bf96cc9a7213bb1b6be91176bcab07a523a1aeb0cf0c52402120e8",
			OriginalString: "node:20-slim@sha256:7b55dbb5c2bf96cc9a7213bb1b6be91176bcab07a523a1aeb0cf0c52402120e8",
		},
	}

	for i, exp := range expected {
		got := updates[i]

		if got.LineNumber != exp.LineNumber {
			t.Errorf("Update %d (%s): wrong line number. Expected %d, got %d", i, exp.ImageName, exp.LineNumber, got.LineNumber)
		}
		if got.ImageName != exp.ImageName {
			t.Errorf("Update %d: wrong image name. Expected %s, got %s", i, exp.ImageName, got.ImageName)
		}
		if got.OldTag != exp.OldTag {
			t.Errorf("Update %d: wrong tag. Expected %s, got %s", i, exp.OldTag, got.OldTag)
		}
		if got.OldDigest != exp.OldDigest {
			t.Errorf("Update %d: wrong old digest. Expected %s, got %s", i, exp.OldDigest, got.OldDigest)
		}
		if got.OriginalString != exp.OriginalString {
			t.Errorf("Update %d: wrong original string. Expected %s, got %s", i, exp.OriginalString, got.OriginalString)
		}
	}
}
