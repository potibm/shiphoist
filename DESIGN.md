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
A `charmbracelet/huh` multi-select rendered as an aligned table, one row per
distinct update:

```
🔵  grafana         grafana/grafana                       11.6.16 → 11.6.17  pin
🟢  ×8              ghcr.io/potibm/billedapparat          0.11 → 0.12        patch
```

* **Minor, Patch & Pinning:** Automatically pre-selected.
* **Major Updates:** Never pre-selected (🔴), to prevent accidental breaking changes.
* **Controls:** `space` toggles, `ctrl+a` selects all or none, `/` filters, `enter` confirms.

### 2. Progress Reporting
Registry checks are the slow part of a run, so they report on a single updating
line rather than one line per image:

```
[████████████░░░░░░░░░░░░░]  8/18  ghcr.io/potibm/billedapparat
```

A progress **bar** rather than a spinner, because the total is known in advance.
The bar is dropped below 40 columns, and when the output is not a terminal
nothing is drawn at all — only a final summary — so piped output and CI logs
contain no escape sequences. `--verbose` restores one line per image. Registry
failures are collected and printed as a block once the line is erased, so they
cannot break the single line while it is being redrawn.

### 3. CI/CD Mode (Planned: `--yes`)
For pipelines, Makefiles, or automation. Will skip the UI and immediately apply all safe (pre-selected) updates.

---

## 🏗️ Architecture: The Hybrid Approach

To avoid the weaknesses of pure AST parsers (destroy formatting/comments) and dumb regex scanners (false positives), *shiphoist* uses a two-stage hybrid approach:

1. **Discovery Phase (Parser):**
   Real parsers (`github.com/goccy/go-yaml` for Compose, `moby/buildkit` for Dockerfiles) read the file and find validated images. The parser provides the **exact line number**.
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
```

### Identity: why the image name is not enough

`ImageName` holds the repository only, without the tag. The same repository can
legitimately appear in several services with different tags:

```yaml
services:
  db:
    image: postgres:16
  cache:
    image: postgres:16-alpine
```

Anything that needs to tell these two updates apart must therefore use
`ImageUpdate.Key()`, which returns `FilePath:LineNumber` — the same coordinate
the `Patcher` uses to address the line. Selection and patching therefore cannot
disagree. Keying on `ImageName` instead would make deselecting one of the two
services apply the change to both.

`ImageUpdate.Label()` renders `[service-name] image` for user-facing output, and
degrades to just the image name when the source format has no named service.


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

// 4. PROMPTING: Lets the user narrow the selection. Implementations must
//    identify updates by ImageUpdate.Key, never by image name.
type Prompter interface {
	SelectUpdates(updates []ImageUpdate) ([]ImageUpdate, error)
}
```

## Fetch Deduplication

A Compose file that reuses one image across many services would otherwise cost
one registry round-trip per service. References are grouped by
`ImageName + OldTag + OldDigest` and fetched once:

```
services:
  db:     { image: ghcr.io/potibm/billedapparat:0.11 }
  cache:  { image: ghcr.io/potibm/billedapparat:0.11 }   # one lookup, two patches
```

`OldDigest` is part of the key on purpose. A pinned reference and an unpinned
one naming the same `image:tag` take different paths through the fetcher, so
merging their lookups would produce a patch for the wrong reference.

The fan-out then copies **only the resolved fields** onto each member:

```go
member.CurrentDigest, member.NewDigest, member.NewTag, member.UpdateType,
member.Selected, member.OldTagMissing, member.NoCompatibleTags, member.MajorTag
```

Every member keeps its own `FilePath`, `LineNumber`, `ServiceName`,
`OriginalString`, `OldTag` and `OldDigest`, so the patcher still targets that
member's exact line. Returning the representative as-is would rewrite every
duplicate to the first service's line.

The run summary reports both counts, so a deduplicated run is never mistaken for
a truncated one: `✅ Checked 18 unique images (25 references) in 4.2s`.

The TUI applies the mirror-image idea: identical *results* share a row marked
`×8`, and selecting one opens a second prompt to narrow which services apply.
Grouping a result is safe precisely because the fan-out keeps members distinct.

## Terminal Rendering

`internal/ui` holds all presentation logic, free of pipeline concerns so it can
be unit tested without a terminal:

* `table.go` — column layout. Widths are measured in display cells
  (`ansi.StringWidth`), not bytes, so the wide emoji markers do not push every
  following column out of alignment. Columns are padded to their natural width
  and only shortened, widest first, when the layout would overflow. Truncation
  is mandatory rather than cosmetic: `lipgloss` **wraps** content wider than the
  block width, and a wrapped row destroys the alignment of every column below
  it. The marker, version and kind columns are never shrunk, since they carry
  the meaning of the row.
* `progress.go` — the bar reporter, safe for concurrent use because a run
  advances from one goroutine per image.
* `terminal.go` — TTY detection and width via `charmbracelet/x/term`. The bar is
  drawn for real terminals only; anything else gets the summary alone.

Neither `bubbles/spinner` nor `bubbles/progress` is used. Their models animate
through `bubbletea`, which would pull a large dependency into a direct
dependency for the sake of a few characters, and both are trivial to render and
far easier to test directly. `charmbracelet/x/ansi` and `charmbracelet/x/term`
were already in the module graph as indirect dependencies, so promoting them to
direct added no new modules.

## Registry I/O Boundary

`RegistryClient` is a narrow I/O seam below `RegistryFetcher`, which keeps the
SemVer logic testable without a network:

```go
type RegistryClient interface {
	ListTags(ctx context.Context, repo string) ([]string, error)
	GetDigest(ctx context.Context, ref string) (string, error)
}
```

Implementations are layered as decorators:

* `RemoteClient` — thin `go-containerregistry` wrapper.
* `CachedClient` — file-based cache under the user cache dir, with TTL and a
  force-refresh bypass.

Retries are intentionally *not* hand-rolled. `go-containerregistry` already
wraps every call in `transport.NewRetry` (3 attempts, 1s → 3s exponential
backoff with jitter, covering 408/429/5xx), so a custom retry loop in
`RegistryFetcher` would only duplicate it.

## Test Strategy

* **Unit tests never touch the network.** `RegistryClient` is mocked via a
  function-field stub.
* **Live-registry tests are opt-in.** They live in `*_integration_test.go`, are
  skipped under `go test -short`, and assert invariants rather than specific
  upstream tags, which eventually get deleted.
* `internal/engine` is exercised under `-race`, since it fans out one goroutine
  per distinct lookup and merges the results under a mutex.
* Terminal output is asserted on the bytes written, so wrapping, alignment and
  the absence of escape sequences in non-interactive output are all testable.
