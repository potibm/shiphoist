package discovery

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/potibm/shiphoist/internal/core"
)

// multiStage is a realistic Dockerfile exercising every shape the scanner has
// to survive: several stages, a platform flag, a comment, a build argument, a
// base built from another stage, and a build-only instruction.
const multiStage = `# syntax=docker/dockerfile:1
ARG NODE=22
FROM --platform=linux/amd64 node:$NODE AS builder
WORKDIR /app
COPY package.json .
RUN npm ci

FROM node:22-alpine AS deps
RUN apk add --no-cache git

FROM golang:1.22 AS builder2
COPY --from=deps /app /app

FROM scratch
COPY --from=builder2 /out /out

FROM alpine:3.19
RUN echo "FROM nginx:1.25.0 is only text here"
COPY --from=builder /out /
`

func writeDockerfile(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "Dockerfile")
	if err := os.WriteFile(path, []byte(content), testFilePermissions); err != nil {
		t.Fatalf("writing Dockerfile: %v", err)
	}

	return path
}

func discoverDockerfile(t *testing.T, content string) ([]core.ImageUpdate, *DockerfileDiscoverer) {
	t.Helper()

	discoverer := &DockerfileDiscoverer{}

	updates, err := discoverer.Discover(context.Background(), writeDockerfile(t, content))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	return updates, discoverer
}

func TestDockerfileDiscoverer_FindsEveryStage(t *testing.T) {
	updates, _ := discoverDockerfile(t, multiStage)

	// Two of the five FROM lines are deliberately skipped: the one built from a
	// build argument and `scratch`.
	got := make([]string, 0, len(updates))
	for _, u := range updates {
		got = append(got, u.ServiceName)
	}

	want := []string{"deps", "builder2", "stage-2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("expected stages %v, got %v", want, got)
	}
}

func TestDockerfileDiscoverer_ParsesReferences(t *testing.T) {
	updates, _ := discoverDockerfile(t, "FROM node:22-alpine AS deps\n")

	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}

	u := updates[0]

	if u.ImageName != "node" {
		t.Errorf("expected the repository node, got %q", u.ImageName)
	}

	if u.OldTag != "22-alpine" {
		t.Errorf("expected the whole tag 22-alpine, got %q", u.OldTag)
	}

	// The original text must be reproduced exactly, because the patcher
	// replaces it literally.
	if u.OriginalString != "node:22-alpine" {
		t.Errorf("expected the literal reference, got %q", u.OriginalString)
	}

	if u.ServiceName != "deps" {
		t.Errorf("expected the stage alias, got %q", u.ServiceName)
	}
}

func TestDockerfileDiscoverer_ReportsWhatItWouldNotUpdate(t *testing.T) {
	_, discoverer := discoverDockerfile(t, multiStage)

	filtered := discoverer.Filtered()
	if len(filtered) != 2 {
		t.Fatalf("expected 2 filtered references, got %+v", filtered)
	}

	byReason := map[string]core.Filtered{}
	for _, f := range filtered {
		byReason[f.Reason] = f
	}

	variable, ok := byReason[ReasonVariableReference]
	if !ok {
		t.Fatalf("expected a variable reference to be reported, got %+v", filtered)
	}

	// Reported as the reference that was skipped, not just the variable, so the
	// reader can see which image it stood for.
	if variable.Image != "node:$NODE" {
		t.Errorf("expected node:$NODE, got %q", variable.Image)
	}

	// The line number has to be right, since the report points at the file.
	if variable.LineNumber != 3 {
		t.Errorf("expected line 3, got %d", variable.LineNumber)
	}

	if _, ok := byReason[ReasonReservedBase]; !ok {
		t.Errorf("expected scratch to be reported, got %+v", filtered)
	}
}

// COPY --from must never be read as a base image, and text inside a RUN must
// never be either.
func TestDockerfileDiscoverer_IgnoresNonFromLines(t *testing.T) {
	updates, _ := discoverDockerfile(t, multiStage)

	for _, u := range updates {
		if u.ImageName == "builder" || u.ImageName == "deps" {
			t.Errorf("a COPY --from target was treated as a base image: %+v", u)
		}
	}
}

func TestDockerfileDiscoverer_UnnamedStagesAreNumbered(t *testing.T) {
	updates, _ := discoverDockerfile(t, "FROM alpine:3.19\nFROM busybox:1.36\n")

	want := []string{"stage-0", "stage-1"}

	got := make([]string, 0, len(updates))
	for _, u := range updates {
		got = append(got, u.ServiceName)
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func TestDockerfileDiscoverer_EmptyFile(t *testing.T) {
	updates, _ := discoverDockerfile(t, "")

	if len(updates) != 0 {
		t.Errorf("expected no updates, got %+v", updates)
	}
}

func TestDockerfileDiscoverer_OnlyComments(t *testing.T) {
	updates, discoverer := discoverDockerfile(t, "# nothing here\n# FROM nginx:1.25.0\n")

	if len(updates) != 0 {
		t.Errorf("expected no updates, got %+v", updates)
	}

	if got := discoverer.Filtered(); len(got) != 0 {
		t.Errorf("expected nothing filtered, got %+v", got)
	}
}

func TestDockerfileDiscoverer_MissingFile(t *testing.T) {
	discoverer := &DockerfileDiscoverer{}

	_, err := discoverer.Discover(context.Background(), filepath.Join(t.TempDir(), "absent"))
	if err == nil {
		t.Fatal("expected an error")
	}
}

// Filtering must not leak into the next run, as with the Compose path.
func TestDockerfileDiscoverer_FilteredResetsBetweenRuns(t *testing.T) {
	discoverer := &DockerfileDiscoverer{}

	skipped := "FROM $BASE\n"
	kept := "FROM alpine:3.19\n"

	if _, err := discoverer.Discover(context.Background(), writeDockerfile(t, skipped)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := discoverer.Discover(context.Background(), writeDockerfile(t, kept)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := discoverer.Filtered(); len(got) != 0 {
		t.Errorf("expected no filters on the second run, got %+v", got)
	}
}

func TestDockerfileDiscoverer_ImplementsFilteredDiscoverer(t *testing.T) {
	// The CLI reaches the discoverer's skips through this interface, so a
	// Dockerfile run has to be accounted for the same way a Compose run is.
	var discoverer core.Discoverer = &DockerfileDiscoverer{}

	if _, ok := discoverer.(core.FilteredDiscoverer); !ok {
		t.Error("expected DockerfileDiscoverer to satisfy core.FilteredDiscoverer")
	}
}

func TestIsDockerfileName(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: "Dockerfile", want: true},
		{path: "dockerfile", want: true},
		{path: "Dockerfile.prod", want: true},
		{path: "app.Dockerfile", want: true},
		{path: "path/to/Dockerfile", want: true},
		{path: `C:\build\Dockerfile`, want: true},
		{path: "docker-compose.yml"},
		{path: "docker-compose.yaml"},
		{path: "compose.yml"},
		{path: "docker-compose.override.yml"},
		{path: "Dockerfileish.yml"},
		{path: "my.Dockerfile", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := isDockerfileName(tt.path); got != tt.want {
				t.Errorf("isDockerfileName(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestForFile(t *testing.T) {
	tests := []struct {
		path     string
		wantType string
	}{
		{path: "Dockerfile", wantType: "*discovery.DockerfileDiscoverer"},
		{path: "app.Dockerfile", wantType: "*discovery.DockerfileDiscoverer"},
		{path: "docker-compose.yml", wantType: "*discovery.ComposeDiscoverer"},
		{path: "compose.yaml", wantType: "*discovery.ComposeDiscoverer"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := typeName(ForFile(tt.path))

			if got != tt.wantType {
				t.Errorf("expected %s, got %s", tt.wantType, got)
			}
		})
	}
}

// typeName is the concrete type of a value, for asserting which discoverer
// ForFile chose.
func typeName(v any) string {
	return fmt.Sprintf("%T", v)
}
