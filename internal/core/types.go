package core

import (
	"context"
	"fmt"
)

// UpdateType describes the magnitude of the version jump.
type UpdateType string

const (
	UpdateTypePatch UpdateType = "patch"
	UpdateTypeMinor UpdateType = "minor"
	UpdateTypeMajor UpdateType = "major"
	UpdateTypeNone  UpdateType = "none"
)

// Severity ranks an update type, higher meaning riskier. It lives here rather
// than in the UI because both the interactive table and the non-interactive
// selector have to reason about the same ordering, and neither may import the
// other.
const (
	// SeverityUnknown is the rank of an unrecognised type, which sorts below
	// every real one so an unknown value is never treated as the safest.
	SeverityUnknown = 0
	SeverityPin     = 1
	SeverityPatch   = 2
	SeverityMinor   = 3
	SeverityMajor   = 4
)

// Severity returns the rank of an update type.
func Severity(t UpdateType) int {
	switch t {
	case UpdateTypeMajor:
		return SeverityMajor
	case UpdateTypeMinor:
		return SeverityMinor
	case UpdateTypePatch:
		return SeverityPatch
	case UpdateTypeNone:
		return SeverityPin
	default:
		return SeverityUnknown
	}
}

// ImageUpdate carries the state of an update throughout the application.
type ImageUpdate struct {
	// Localization for surgical patching
	FilePath   string `json:"file_path"`
	LineNumber int    `json:"line_number"`

	// Identity of the declaration site. For a Compose file this is the service
	// name; for a Dockerfile it is the build stage.
	ServiceName string `json:"service_name"`

	// Current state
	OriginalString string `json:"original_string"`
	ImageName      string `json:"image"`
	OldTag         string `json:"old_tag"`
	OldDigest      string `json:"old_digest"` // Holds the existing hash (e.g., "sha256:abcdef...")

	// Target state (from registry)
	NewTag     string     `json:"new_tag"`
	NewDigest  string     `json:"new_digest"`
	UpdateType UpdateType `json:"update_type"`

	// UI state
	//
	// Selected is the initial pre-selection: true for patch and minor, false
	// for major, which must be opted into.
	Selected bool `json:"selected"`

	// OldTagMissing reports that the current tag no longer exists in the
	// registry (404).
	OldTagMissing bool `json:"old_tag_missing"`

	// CurrentDigest is the registry-resolved digest of the current (old) tag.
	CurrentDigest string `json:"current_digest"`

	// NoCompatibleTags reports that ListTags succeeded but found no
	// SemVer-compatible candidates.
	NoCompatibleTags bool `json:"no_compatible_tags"`

	// MajorTag is the newest tag when it would be a major bump. It is reported
	// but never pre-selected.
	MajorTag string `json:"major_tag"`
}

// Key uniquely identifies an update by its declaration site.
//
// The image name alone is not unique: the same repository may appear in
// several services with different tags, and keying on it would make
// deselecting one of them apply to all of them. FilePath and LineNumber are
// also how the Patcher addresses the line, so selection and patching can
// never disagree.
func (u ImageUpdate) Key() string {
	if u.FilePath == "" && u.LineNumber == 0 {
		return u.ImageName
	}

	return fmt.Sprintf("%s:%d", u.FilePath, u.LineNumber)
}

// Label renders the image for user-facing output, prefixed with the service
// name when the source format provides one.
func (u ImageUpdate) Label() string {
	if u.ServiceName == "" {
		return u.ImageName
	}

	return fmt.Sprintf("[%s] %s", u.ServiceName, u.ImageName)
}

// ---------------------------------------------------------
// Core Interfaces
// ---------------------------------------------------------

// Discoverer finds images and their exact line numbers.
// Implementations: ComposeDiscoverer, DockerfileDiscoverer.
type Discoverer interface {
	Discover(ctx context.Context, filePath string) ([]ImageUpdate, error)
}

// RegistryFetcher compares found images with the registry,
// executes SemVer logic, and retrieves SHA256 digests.
type RegistryFetcher interface {
	FetchUpdate(ctx context.Context, current ImageUpdate) (ImageUpdate, error)
}

// Patcher handles non-destructive modification of source files
// by modifying only the exact lines identified in the ImageUpdate.
type Patcher interface {
	Patch(ctx context.Context, filePath string, updates []ImageUpdate) error
}

// Prompter lets the user choose which discovered updates to apply.
// Implementations must identify updates by ImageUpdate.Key, never by image
// name, so that two services sharing a repository stay independent.
type Prompter interface {
	SelectUpdates(updates []ImageUpdate) ([]ImageUpdate, error)
}
