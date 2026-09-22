package core

import "context"

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

type Prompter interface {
	SelectUpdates(updates []ImageUpdate) ([]ImageUpdate, error)
}
