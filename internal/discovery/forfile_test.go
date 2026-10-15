package discovery

import (
	"testing"

	"github.com/potibm/shiphoist/internal/core"
)

// ForFile promises a core.Discoverer, whatever name it is handed.
var _ core.Discoverer = ForFile("docker-compose.yml")

func TestForFile_SelectsTheDiscovererByName(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantDocker bool
	}{
		{name: "conventional name", path: "Dockerfile", wantDocker: true},
		{name: "lowercase name", path: "dockerfile", wantDocker: true},
		{name: "directory prefix", path: "build/Dockerfile", wantDocker: true},
		{name: "generated suffix", path: "Dockerfile.prod", wantDocker: true},
		{name: "app convention", path: "app.Dockerfile", wantDocker: true},
		{name: "compose file", path: "docker-compose.yml", wantDocker: false},
		{name: "plain compose", path: "compose.yaml", wantDocker: false},
		{name: "compose override", path: "docker-compose.override.yml", wantDocker: false},
		{name: "mixed case", path: "My.DOCKERFILE", wantDocker: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ForFile(tt.path)

			if _, isDockerfile := got.(*DockerfileDiscoverer); isDockerfile != tt.wantDocker {
				t.Errorf("path %q: expected a Dockerfile discoverer: %t, got %T", tt.path, tt.wantDocker, got)
			}
		})
	}
}

// An unusable name must not be silently read as the other format: treating a
// Dockerfile as Compose would find no images at all, and a run over it would
// report "up to date" for a file holding dozens of references.
func TestForFile_UnknownNamesFallBackToCompose(t *testing.T) {
	for _, path := range []string{"images.txt", "k8s/deployment.yaml", "Dockerfileish.yml", ""} {
		got := ForFile(path)

		if _, ok := got.(*ComposeDiscoverer); !ok {
			t.Errorf("path %q: expected the Compose discoverer, got %T", path, got)
		}
	}
}

// Each call returns its own instance, so a run over one file can never inherit
// the filtered list of the previous one.
func TestForFile_ReturnsIndependentInstances(t *testing.T) {
	first, second := ForFile("Dockerfile"), ForFile("Dockerfile")
	if first == second {
		t.Fatal("expected a fresh discoverer per call")
	}
}
