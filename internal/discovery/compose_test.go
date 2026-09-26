package discovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/potibm/shiphoist/internal/core"
)

const testFilePermissions = 0o600

func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), testFilePermissions); err != nil {
		t.Fatalf("could not write temp file: %v", err)
	}

	return path
}

func discover(t *testing.T, content string) []core.ImageUpdate {
	t.Helper()

	return discoverFile(t, writeTempFile(t, "docker-compose.yaml", content))
}

func discoverFile(t *testing.T, path string) []core.ImageUpdate {
	t.Helper()

	updates, err := (&ComposeDiscoverer{}).Discover(context.Background(), path)
	if err != nil {
		t.Fatalf("unexpected error during Discover: %v", err)
	}

	return updates
}

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

	expected := []core.ImageUpdate{
		{
			LineNumber:     7,
			ServiceName:    "web",
			ImageName:      "nginx",
			OldTag:         "1.25.0-alpine",
			OldDigest:      "",
			OriginalString: "nginx:1.25.0-alpine",
		},
		{
			LineNumber:     12,
			ServiceName:    "db",
			ImageName:      "postgres",
			OldTag:         "15-bullseye",
			OldDigest:      "",
			OriginalString: "postgres:15-bullseye",
		},
		{
			LineNumber:     20,
			ServiceName:    "redis",
			ImageName:      "redis",
			OldTag:         "7.2",
			OldDigest:      "",
			OriginalString: "redis:7.2",
		},
		{
			LineNumber:     24,
			ServiceName:    "worker",
			ImageName:      "node",
			OldTag:         "20-slim",
			OldDigest:      "sha256:7b55dbb5c2bf96cc9a7213bb1b6be91176bcab07a523a1aeb0cf0c52402120e8",
			OriginalString: "node:20-slim@sha256:7b55dbb5c2bf96cc9a7213bb1b6be91176bcab07a523a1aeb0cf0c52402120e8",
		},
	}

	got := discover(t, yamlContent)

	if len(got) != len(expected) {
		t.Fatalf("expected %d updates, got %d", len(expected), len(got))
	}

	for i, exp := range expected {
		assertUpdate(t, i, got[i], exp)
	}
}

func assertUpdate(t *testing.T, index int, got, exp core.ImageUpdate) {
	t.Helper()

	if got.LineNumber != exp.LineNumber {
		t.Errorf("update %d (%s): expected line %d, got %d", index, exp.ImageName, exp.LineNumber, got.LineNumber)
	}

	if got.ServiceName != exp.ServiceName {
		t.Errorf("update %d: expected service %q, got %q", index, exp.ServiceName, got.ServiceName)
	}

	if got.ImageName != exp.ImageName {
		t.Errorf("update %d: expected image %q, got %q", index, exp.ImageName, got.ImageName)
	}

	if got.OldTag != exp.OldTag {
		t.Errorf("update %d: expected tag %q, got %q", index, exp.OldTag, got.OldTag)
	}

	if got.OldDigest != exp.OldDigest {
		t.Errorf("update %d: expected old digest %q, got %q", index, exp.OldDigest, got.OldDigest)
	}

	if got.OriginalString != exp.OriginalString {
		t.Errorf("update %d: expected original string %q, got %q", index, exp.OriginalString, got.OriginalString)
	}

	if got.FilePath == "" {
		t.Errorf("update %d: expected FilePath to be set", index)
	}
}

func TestComposeDiscoverer_Discover_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected []core.ImageUpdate
	}{
		{
			name: "no services key at all",
			content: `version: '3.8'
volumes:
  data:
`,
			expected: nil,
		},
		{
			name: "empty services map",
			content: `version: '3.8'
services:
`,
			expected: nil,
		},
		{
			name: "services with no images",
			content: `version: '3.8'
services:
  web:
    build: .
    ports:
      - "80:80"
  db:
    build:
      context: ./db
`,
			expected: nil,
		},
		{
			name: "build and image together yields the image",
			content: `services:
  web:
    build: .
    image: nginx:1.25.0
`,
			expected: []core.ImageUpdate{{
				LineNumber:     4,
				ServiceName:    "web",
				ImageName:      "nginx",
				OldTag:         "1.25.0",
				OriginalString: "nginx:1.25.0",
			}},
		},
		{
			name:    "untagged image defaults to latest",
			content: "services:\n  web:\n    image: nginx\n",
			expected: []core.ImageUpdate{
				{LineNumber: 3, ServiceName: "web", ImageName: "nginx", OldTag: "latest", OriginalString: "nginx"},
			},
		},
		{
			name:    "registry with a port",
			content: "services:\n  web:\n    image: localhost:5000/app:1.2.3\n",
			expected: []core.ImageUpdate{
				{
					LineNumber:     3,
					ServiceName:    "web",
					ImageName:      "localhost:5000/app",
					OldTag:         "1.2.3",
					OriginalString: "localhost:5000/app:1.2.3",
				},
			},
		},
		{
			name:     "image key with no scalar value is skipped",
			content:  "services:\n  web:\n    image:\n    restart: always\n",
			expected: nil,
		},
		{
			name:     "service whose value is not a mapping",
			content:  "services:\n  - just-a-string\n",
			expected: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := discover(t, tc.content)

			if len(got) != len(tc.expected) {
				t.Fatalf("expected %d updates, got %d: %+v", len(tc.expected), len(got), got)
			}

			for i, exp := range tc.expected {
				assertUpdate(t, i, got[i], exp)
			}
		})
	}
}

// The same repository in two services must yield two distinct, independently
// addressable updates rather than being deduplicated or conflated.
func TestComposeDiscoverer_Discover_SameRepoInTwoServices(t *testing.T) {
	yamlContent := `services:
  db:
    image: postgres:16
  cache:
    image: postgres:16-alpine`

	got := discover(t, yamlContent)

	if len(got) != 2 {
		t.Fatalf("expected 2 updates, got %d: %+v", len(got), got)
	}

	if got[0].ServiceName != "db" || got[1].ServiceName != "cache" {
		t.Errorf("unexpected service names: %q and %q", got[0].ServiceName, got[1].ServiceName)
	}

	if got[0].Key() == got[1].Key() {
		t.Errorf("expected distinct keys for the same repo, both were %q", got[0].Key())
	}

	if got[0].OldTag != "16" || got[1].OldTag != "16-alpine" {
		t.Errorf("unexpected tags: %q and %q", got[0].OldTag, got[1].OldTag)
	}
}

// The identical image referenced twice on separate lines must still resolve to
// two separate line addresses.
func TestComposeDiscoverer_Discover_SameImageTwice(t *testing.T) {
	yamlContent := `services:
  db:
    image: postgres:16
  reporting:
    image: postgres:16`

	got := discover(t, yamlContent)

	if len(got) != 2 {
		t.Fatalf("expected 2 updates, got %d: %+v", len(got), got)
	}

	if got[0].LineNumber == got[1].LineNumber {
		t.Errorf("expected distinct line numbers, both were %d", got[0].LineNumber)
	}

	if got[0].Key() == got[1].Key() {
		t.Errorf("expected distinct keys, both were %q", got[0].Key())
	}
}

func TestComposeDiscoverer_Discover_Errors(t *testing.T) {
	t.Run("non-existent file", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "does-not-exist.yaml")

		_, err := (&ComposeDiscoverer{}).Discover(context.Background(), missing)
		if err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("invalid yaml", func(t *testing.T) {
		path := writeTempFile(t, "docker-compose.yaml", "services:\n  web:\n   image: \"unterminated\n  :\n\t- x\n")

		_, err := (&ComposeDiscoverer{}).Discover(context.Background(), path)
		if err == nil {
			t.Fatal("expected an error for malformed YAML")
		}
	})
}

// A file that is valid YAML but not a Compose file must not error out; there
// is simply nothing to find.
func TestComposeDiscoverer_Discover_NonComposeFile(t *testing.T) {
	path := writeTempFile(t, "random.yaml", "just: a mapping\nwith: some keys\n")

	updates := discoverFile(t, path)

	if len(updates) != 0 {
		t.Errorf("expected no updates, got %+v", updates)
	}
}
