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

### 3. CI/CD Mode (`--yes`, optionally with `--dry-run` or `--json`)
For pipelines, Makefiles, or automation. Skips the form and applies every update
within the `--mode` cap. See "Update Caps" below.

## Update Caps

`--mode` names the largest jump that may be applied. The ranking lives in
`core.Severity` rather than in the UI, because the interactive table and the
non-interactive selector both have to reason about it and neither may import the
other:

```go
SeverityMajor > SeverityMinor > SeverityPatch > SeverityPin > SeverityUnknown
```

A cap is enforced differently in the two modes, and the difference is the whole
design:

| | Interactively | With `--yes` |
| --- | --- | --- |
| Row above the cap | shown, unchecked | not offered at all |
| Why | the user can see a bigger jump exists and opt into it deliberately | there is nobody to ask, so "not pre-selected" can only mean "not applied" |

`internal/prompting.Apply` is therefore a different type from the TUI, not a
configured instance of it. Filtering the TUI's input would have hidden the rows
and thrown away the information the cap exists to preserve.

`updateGroup.safeToApply` is deliberately two conditions:

```go
severity < SeverityMajor && severity <= Severity(maxUpdate)
```

The first is a floor, the second a ceiling. A cap is something the user asked
for, so it may only tighten the automatic selection; it must never be able to
reopen the major-update safety floor. Collapsing this to a single comparison
would let `--mode major` pre-select a major bump.

## Non-Interactive Operation

`--yes` substitutes `prompting.Apply` for the TUI. `--dry-run` sets
`Pipeline.DryRun`, which resolves and reports the selection but never calls the
`Patcher`, so the report names precisely the changes a real run would write.

`--quiet` adds a `modeQuiet` to `ui.Progress`, which writes nothing at all — not
the bar, not the summary, not the failure block. It still *returns* the
collected failures, because the report and the exit code are built from them;
hiding the chatter must not lose the record.

### stdout and stderr

`--json` gives stdout to the report and moves every human-facing line to stderr
via one helper, `humanOut`. The result table and banner follow, so a CI log
shows what was attempted next to the machine-readable result. This is why
`Pipeline.Out` and the CLI's own printing are wired to the same writer choice
rather than to `os.Stdout` directly.

`--json` and `--verbose` are rejected together: both promise one line per image,
and honouring both would corrupt the document.

### When there is no terminal

Without a TTY, and without `--yes`, shiphoist fails with advice rather than
letting the form library produce an opaque error. The check is `ui.CanPrompt`,
which asks exactly the question the renderer answers: a real terminal, **or**
`TERM=dumb`, where `huh` switches to its accessible numbered list. Asking a
stricter question than the renderer does would refuse a prompt that works.

### Exit codes

An unresolvable image is a *report outcome*, not a failure of shiphoist: healthy
images are still updated and the rest are listed in `Report.Failures`. Whether
that also fails the process depends on the mode:

| Mode | Unresolved images | Exit |
| --- | --- | --- |
| interactive | reported, run continues | `0` |
| `--yes` / `--json` | reported, run continues | `1` |

`--yes` and `--json` are the modes a pipeline uses, so they are the modes where an
image nobody could check has to fail the build. The sentinel `errIncompleteRun`
carries that without a message, because `main` must not print it: the report
already names the images, and printing an error line too would be duplication.

## 🏗️ Architecture: The Hybrid Approach

To avoid the weaknesses of pure AST parsers (destroy formatting/comments) and dumb regex scanners (false positives), *shiphoist* uses a two-stage hybrid approach:

1. **Discovery Phase (Parser):**
   Real parsers read the file and find validated images. The parser provides the **exact line number**. Compose files are parsed with `github.com/goccy/go-yaml` into an AST. Dockerfiles are not parsed with a dedicated dependency — a hand-rolled line scanner handles them, which keeps the module graph small and yields line numbers directly.
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


## The Run Report

`Pipeline.ProcessFile` returns a `core.Report` rather than a bare slice of
updates. A slice cannot express the outcomes a caller legitimately needs to act
on: an image that could not be checked, a run that checked fewer images than it
found references because they deduplicated, or a run that resolved everything
but wrote nothing.

```go
type Report struct {
    File       string        `json:"file"`
    Updates    []ImageUpdate `json:"updates"`
    Failures   []Failure     `json:"failures"`
    Checked    int           `json:"checked"`     // distinct registry lookups
    References int           `json:"references"`  // declarations found
    ElapsedMS  int64         `json:"elapsed_ms"`
    Written    bool          `json:"written"`
    DryRun     bool          `json:"dry_run"`
}
```

Two decisions are load-bearing:

* **A failure is not an error.** A registry that 401s on one image is reported
  in `Failures` and skipped, and the run still patches everything else. An
  `error` return means the *run* could not complete — bad YAML, an unwritable
  file, a cancelled context — and then no report is returned at all. This keeps
  "some images failed" separable from "shiphoist did not run".
* **`Written` and `DryRun` are separate.** A dry run that resolved three updates
  reports `dry_run: true, written: false`; a run where the user deselected
  everything reports `dry_run: false, written: false`. A single boolean could not
  tell those apart, and a CI job needs to.

`MarshalJSON` normalises nil slices to `[]`. A report built by any route
serialises to the same shape, so a machine reader never has to handle both `[]`
and `null`. The guarantee lives on the type rather than in a constructor
precisely so it cannot be forgotten.

`Report` is also the payload a future `--json` serialises verbatim, which is why
the tags are snake_case and every field is present: adding a field to a JSON
document is safe, renaming one is not.

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

## Filtering References

An image can be left alone two ways, and they are implemented at different
layers on purpose.

**`# shiphoist-ignore` lives in `ComposeDiscoverer`.** The comment only exists in
the source text and is gone once the file has been reduced to tokens, so no
decorator could honour it — by the time an outer layer sees an `ImageUpdate`,
the comment has already been discarded. The discoverer already holds the raw
bytes, so it reads the line back.

It is read from the source line starting at the token's `Column`, not from the
AST. `goccy/go-yaml` attaches comments through two different functions
(`setLineComment` and `setHeadComment`) depending on which parser path built the
node, so which node holds the comment is not a stable contract. Scanning the
text avoids the question and is more precise besides: anything before the
reference is YAML, and cannot be a trailing comment on it.

The directive must be the **first thing in the comment**, with an optional free-form
reason after it. A substring match would treat `# TODO shiphoist-ignore later` as
a directive, and silently skipping an image the user still wants updated is the
worse error. The line is still inspected when the column is unusable, because
missing a directive that is there means rewriting an image the user asked us to
leave alone.

**`--exclude` is a `Discoverer` decorator.** Matching a repository needs only
`ImageName`, so it is format-agnostic and a decorator is the right shape: the
Dockerfile discoverer will get it for free.

The pattern is matched against the repository, never the tag. Excluding a
registry or a namespace is what the flag is for, and matching the full reference
would make `^nginx$` silently match nothing, since the reference is really
`nginx:1.25.0`.

### Accounting for what was skipped

`Report.References` counts what discovery *returned*, so a filtered run would
under-report the file. `Report.Filtered` carries the skipped references with
their line numbers and reasons, which is what makes "why did CI not update X?"
answerable. `Report.References` plus `len(Report.Filtered)` is the file's true
image count.

A discoverer reports its own skips through `core.FilteredDiscoverer`, and
`ExcludeDiscoverer.Filtered` returns its skips *plus* the inner discoverer's, so
a caller only ever asks the outermost one. The engine does not know any of this:
the CLI assembles the chain, so the CLI is what completes the report with it.

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
  advances from one goroutine per image. Its four modes are `modeQuiet`,
  `modeSilent`, `modeBar` and `modeVerbose`, in increasing order of insistence,
  so `--quiet` needs no special casing at the call sites.
* `terminal.go` — TTY detection and width via `charmbracelet/x/term`, plus
  `CanPrompt`, which answers "can a form be answered here?" rather than "is this
  a TTY?". The bar is drawn for real terminals only; anything else gets the
  summary alone.

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
* **The CLI is tested through its real entry point.** `cmd/shiphoist` builds the
  actual `cobra` command and drives it via `SetArgs`, with `deps` supplying
  buffers and stubs. Commands return errors rather than calling `os.Exit`, so
  `main` holds the only exit in the program and every branch below it is
  reachable from a test. A dedicated test asserts the command stays silent on
  failure, so a passing suite also proves nothing is written straight to the
  process.
* **The interactive form is tested for real, without a terminal.** `huh` in
  accessible mode (`WithAccessible` + `WithInput` + `WithOutput`) renders a
  numbered list and reads line input, so a blank line confirms whatever
  shiphoist preselected. That is the only assertion made: it covers the
  pre-selection policy, the grouping and the drill-down, while deliberately
  *not* covering interactive toggling, which is `huh`'s behaviour rather than
  shiphoist's.
