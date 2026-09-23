# 🚢 shiphoist

**shiphoist** is a lightweight, secure Go CLI tool to update Docker image versions in `docker-compose.yaml` files, pinning them immutably via SHA256 digest.

The maritime concept: A ship (container) enters the lift, is carefully raised to the next water level (the new version), and exits safely and immutably anchored.

---

## 🎯 Core Principles

1. **Secure by Default:** All updates are pinned with a SHA256 digest by default (`image:tag@sha256:...`).
2. **Surgical Patching (Non-Destructive):** Manual code, indentation, and especially comments (like `# TODO: keep this version`) are 100% preserved when saving.
3. **Smart SemVer:** Detects image flavors (e.g., `-alpine`) and cleanly distinguishes between major, minor, and patch updates.

---

## 💻 User Experience (UX)

### 1. Interactive Mode (Default)
Uses a checkbox UI (e.g., `charmbracelet/huh` or `survey`), grouped by source files.
* **Minor & Patch Updates:** Automatically pre-selected.
* **Major Updates:** Disabled by default and marked with a warning (⚠️) to prevent accidental breaking changes.

### 2. CI/CD Mode (Planned: `--yes`)
For pipelines, Makefiles, or automation. Will skip the UI and immediately apply all safe (pre-selected) updates.

---

## 🏗️ Architecture: The Hybrid Approach

To avoid the weaknesses of pure AST parsers (destroy formatting/comments) and dumb regex scanners (false positives), *shiphoist* uses a two-stage hybrid approach:

1. **Discovery Phase (Parser):**
   Real parsers (e.g., `yaml.v3` for Compose, `moby/buildkit` for Dockerfiles) read the file and find validated images. The parser provides the **exact line number**.
2. **Patching Phase (Regex/Line-Scanner):**
   The file is read as raw text. A patcher jumps precisely to the determined line number and performs the regex replacement of the tag *only there*.

---

## 🔍 Version Discovery & SemVer Logic

Finding the "newest safe version" is a multi-stage process. *shiphoist* uses the robust Go library `github.com/Masterminds/semver/v3`.

### 1. Fetch Tag List
The tool connects to the respective Docker registry (Docker Hub, GHCR, etc.) and retrieves the complete list of available tags for an image.

### 2. Two-Pass Suffix Matching (Flavors & OS Codenames)
Docker tags often contain "flavors" (e.g., `-alpine`, `-slim`, or Debian releases like `-bullseye`, `-bookworm`). To avoid breaking infrastructure, *shiphoist* uses a two-phase system:

* **Pass 1 (Strict Match):** The tool searches for newer versions with the *exact same* flavor.
  * *Example:* `redis:7.2-alpine` ➡️ searches for `7.4-alpine`. The "bare" `7.4` is ignored here. This guarantees safe minor and patch updates.
* **Pass 2 (Cross-Flavor / Major Discovery):** OS codenames often change with major updates (e.g., when `postgres` switches from `-bullseye` to `-bookworm`). If *shiphoist* doesn't find a new major version in the first pass, it searches in the second pass for new major tags *without* the old flavor or with newer OS codenames.
  * These findings are specially marked as `Major (Flavor Change)` in the UI and are—like all major updates—disabled by default to force manual review.
* Non-SemVer tags like `latest`, `edge`, or `nightly` are fundamentally ignored during automatic search.

### 3. SemVer Parsing & Comparison
The filtered list of tags is parsed and sorted in descending order by `Masterminds/semver/v3`. The library enables us to determine the highest stable version and calculate the version jump exactly:
* `v1.2.3` ➡️ `v1.2.4` = **Patch** (Selected: true)
* `v1.2.3` ➡️ `v1.3.0` = **Minor** (Selected: true)
* `v1.2.3` ➡️ `v2.0.0` = **Major** (Selected: false ⚠️)

### 4. Digest Resolution
Once the new target tag is determined, *shiphoist* specifically fetches the manifest for this tag from the registry to extract the cryptographic SHA256 digest for "immutable pinning".

---

## 📦 Data Model & Interfaces

### The Core Struct
The `ImageUpdate` struct transports the state of an update through the entire application:

```go
// UpdateType describes the magnitude of the version jump
type UpdateType string

const (
	UpdateTypePatch UpdateType = "patch"
	UpdateTypeMinor UpdateType = "minor"
	UpdateTypeMajor UpdateType = "major"
	UpdateTypeNone  UpdateType = "none"
)

type ImageUpdate struct {
	// Localization for surgical patching
	FilePath   string 
	LineNumber int    

	// Current state
	OriginalString string 
	ImageName      string 
	OldTag         string 

	// Target state (from registry)
	NewTag         string     
	NewDigest      string     
	UpdateType     UpdateType 

	// UI control
	Selected         bool   // Initially selected (True for Patch/Minor)
	OldTagMissing    bool   // True if the current tag no longer exists in the registry (404)
	CurrentDigest    string // Registry-resolved digest of the current (old) tag
	NoCompatibleTags bool   // True if ListTags succeeded but no SemVer-compatible candidates found
	MajorTag         string // Newest tag if it's a major bump (not auto-selected)
}
```


## Core Interfaces

Clean separation of responsibilities for easy extensibility and testability.

```go
import "context"

// 1. DISCOVERY: Finds images and their line numbers (Compose/Dockerfile)
type Discoverer interface {
	Discover(ctx context.Context, filePath string) ([]ImageUpdate, error)
}

// 2. REGISTRY: Compares SemVer and fetches SHA256 digests
type RegistryFetcher interface {
	FetchUpdate(ctx context.Context, current ImageUpdate) (ImageUpdate, error)
}

// 3. PATCHING: Replaces strings with line-precision in raw text
type Patcher interface {
	Patch(ctx context.Context, filePath string, updates []ImageUpdate) error
}
```
