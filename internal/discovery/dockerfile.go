package discovery

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/potibm/shiphoist/internal/core"
)

// Dockerfile names that select the Dockerfile discoverer.
const (
	// dockerfileName is the conventional name.
	dockerfileName = "dockerfile"

	// dockerfileSuffix matches the `<something>.Dockerfile` convention.
	dockerfileSuffix = ".dockerfile"
)

// Reasons a Dockerfile reference is reported without being checked.
const (
	// ReasonVariableReference is a FROM whose image comes from a build
	// argument, which --build-arg can override at build time.
	ReasonVariableReference = "variable-reference"

	// ReasonReservedBase is Docker's `scratch`, which has no tags to update.
	ReasonReservedBase = "reserved-base"
)

// DockerfileDiscoverer finds base images in a Dockerfile.
//
// It scans lines rather than pulling in a Dockerfile parser: the only
// instruction that matters is FROM, a line scan yields exact line numbers for
// free, and the existing line-based Patcher already needs them. It also keeps
// the module graph small, the same reasoning that avoided pulling in a
// full-screen TUI model for a few characters.
type DockerfileDiscoverer struct {
	filtered []core.Filtered
}

// Discover returns every updatable base image, in file order.
func (d *DockerfileDiscoverer) Discover(_ context.Context, filePath string) ([]core.ImageUpdate, error) {
	d.filtered = nil

	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", filePath, err)
	}
	defer file.Close()

	lines, err := readLines(file)
	if err != nil {
		return nil, fmt.Errorf("error reading file %s: %w", filePath, err)
	}

	updates := make([]core.ImageUpdate, 0, len(lines))

	for i, line := range lines {
		update, ok := d.fromLine(filePath, line, i+1, len(updates))
		if !ok {
			continue
		}

		updates = append(updates, update)
	}

	return updates, nil
}

// Filtered returns the references that were seen but deliberately not updated.
func (d *DockerfileDiscoverer) Filtered() []core.Filtered {
	return append([]core.Filtered(nil), d.filtered...)
}

// fromLine turns one line into an update, reporting false when it holds no
// reference worth checking. stage is the zero-based index among the stages
// found so far, used to number an unnamed one.
func (d *DockerfileDiscoverer) fromLine(
	filePath, line string,
	lineNumber, stage int,
) (core.ImageUpdate, bool) {
	statement, ok := parseFrom(line)
	if !ok {
		return core.ImageUpdate{}, false
	}

	if isVariableReference(statement.Image) {
		d.record(statement.Image, lineNumber, ReasonVariableReference)

		return core.ImageUpdate{}, false
	}

	if isReservedBase(statement.Image) {
		d.record(statement.Image, lineNumber, ReasonReservedBase)

		return core.ImageUpdate{}, false
	}

	imageName, oldTag, oldDigest := ParseImageReference(statement.Image)

	return core.ImageUpdate{
		FilePath:       filePath,
		LineNumber:     lineNumber,
		ServiceName:    stageName(statement.Alias, stage),
		OriginalString: statement.Image,
		ImageName:      imageName,
		OldTag:         oldTag,
		OldDigest:      oldDigest,
	}, true
}

// record notes a reference the run deliberately skipped, so it is reported
// rather than silently dropped.
func (d *DockerfileDiscoverer) record(image string, lineNumber int, reason string) {
	d.filtered = append(d.filtered, core.Filtered{
		Image:      image,
		LineNumber: lineNumber,
		Reason:     reason,
	})
}

// stageName labels a stage for display. An unnamed stage is numbered in file
// order so the user can still tell two of them apart.
func stageName(alias string, stage int) string {
	if alias != "" {
		return alias
	}

	return fmt.Sprintf("stage-%d", stage)
}

// readLines reads a file into lines.
func readLines(file *os.File) ([]string, error) {
	var lines []string

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	return lines, scanner.Err()
}

// isDockerfileName reports whether a path names a Dockerfile rather than a
// Compose file.
//
// `Dockerfile`, `Dockerfile.prod` and `app.Dockerfile` are all Dockerfiles. The
// Compose files in the wild are lowercase and carry a `.yml` extension, so
// there is no realistic collision.
func isDockerfileName(path string) bool {
	name := path
	if idx := strings.LastIndexAny(name, `/\`); idx >= 0 {
		name = name[idx+1:]
	}

	lowered := strings.ToLower(name)

	return lowered == dockerfileName ||
		strings.HasPrefix(lowered, dockerfileName+".") ||
		strings.HasSuffix(lowered, dockerfileSuffix)
}
