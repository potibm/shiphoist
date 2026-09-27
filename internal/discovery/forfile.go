package discovery

import (
	"github.com/potibm/shiphoist/internal/core"
)

// ForFile returns the discoverer that understands the given file.
//
// The format is chosen from the file name rather than a flag, so the obvious
// invocation works without ceremony: `shiphoist Dockerfile` and
// `shiphoist docker-compose.yml`. Anything that is not recognisably a Dockerfile
// is treated as Compose, which is what the overwhelming majority of files are.
func ForFile(path string) core.Discoverer {
	if isDockerfileName(path) {
		return &DockerfileDiscoverer{}
	}

	return &ComposeDiscoverer{}
}
