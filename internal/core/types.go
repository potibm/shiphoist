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

// ImageUpdate carries the state of an update throughout the application.
type ImageUpdate struct {
	// Localization for surgical patching
	FilePath   string
	LineNumber int

	// Identity of the declaration site. Empty for formats without named
	// services (e.g. a Dockerfile stage).
	ServiceName string

	// Current state
	OriginalString string
	ImageName      string
	OldTag         string
	OldDigest      string // Holds the existing hash (e.g., "sha256:abcdef...")

	// Target state (from registry)
	NewTag     string
	NewDigest  string
	UpdateType UpdateType

	// UI state
	Selected         bool   // Initially selected (True for patch/minor)
	OldTagMissing    bool   // True if the current tag no longer exists in the registry (404)
	CurrentDigest    string // Registry-resolved digest of the current (old) tag
	NoCompatibleTags bool   // True if ListTags succeeded but no SemVer-compatible candidates found
	MajorTag         string // Newest tag if it's a major bump (not auto-selected)
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
