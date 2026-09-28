# 🚢 Shiphoist

An interactive CLI tool to surgically update Docker image tags in Compose files, with SHA256 digest pinning and smart version resolution.

## Features

- **Surgical Patching:** Preserves formatting, indentations, and inline comments in your Compose files.
- **SHA256 Digest Pinning:** Immutably pins updated images to their cryptographic digest.
- **Smart SemVer Resolution:** Accurately distinguishes between patch, minor, and major version jumps while preserving image flavors (e.g. `-alpine`).
- **Interactive TUI:** Review and toggle individual updates in an aligned table before writing changes to disk. Identical updates shared across services collapse into a single row, and a second prompt lets you narrow which services to apply.
- **Live Progress Bar:** Registry checks report on one updating line, so a 25-image file no longer scrolls past.
- **Fetch Deduplication:** A repository used by eight services costs one registry round-trip, not eight.
- **CI Ready:** `--yes`, `--dry-run`, `--mode`, `--quiet` and a `--json` report on stdout make shiphoist usable as a non-interactive gate.
- **Single Image Inspector:** Check registry update candidates for any container image without touching a file.
- **Dockerfile Support:** Update base images in Compose files *and* Dockerfiles, with the format detected from the file name. Multi-stage builds, `AS` aliases and `--platform` flags are handled.
- **Caching:** Registry queries are cached locally for 15 minutes to keep repeated runs fast.

## Installation

Download the pre-built binary for your platform from the [Releases](https://github.com/potibm/shiphoist/releases) page, or install via Go:

```bash
go install github.com/potibm/shiphoist/cmd/shiphoist@latest
```

## Usage

### Interactive Compose Update

Run `shiphoist` against your Compose file:

```bash
shiphoist docker-compose.yml
```
*Note: Minor and patch updates are pre-selected by default; major updates require explicit selection.*

Updates are listed as an aligned table, one row per distinct update:

```
🔵  grafana         grafana/grafana                       11.6.16 → 11.6.17  pin
🟡  otel-collector  otel/opentelemetry-collector-contrib  0.156.0 → 0.157.0  minor
🟢  ×8              ghcr.io/potibm/billedapparat          0.11 → 0.12        patch
```

A repository reused across several services collapses into one row marked `×8`. Select it to apply to all of them; selecting it and then deselecting individual services on the follow-up screen applies to just those. Repositories used with *different* tags (for example `postgres:16` and `postgres:16-alpine`) stay on separate rows.

Controls: `space` toggles a row, `ctrl+a` selects all or none, `/` filters, `enter` confirms.

### Dockerfiles

The format is detected from the file name, so a Dockerfile needs no extra flag:

```bash
shiphoist Dockerfile
shiphoist Dockerfile.prod
shiphoist path/to/app.Dockerfile
```

Each build stage is offered as its own row, labelled with the stage name. Multi-stage builds, `AS` aliases, `--platform` flags and trailing comments are all handled, and only the reference on the `FROM` line is rewritten — the instruction, its flags, the alias and the comment survive untouched:

```dockerfile
FROM --platform=linux/amd64 golang:1.27@sha256:3680233e... AS compile
COPY --from=deps /app /app
FROM scratch
```

Two kinds of `FROM` are deliberately left alone and reported rather than changed:

- **Build arguments** — `FROM node:$NODE_VERSION`. The `ARG` default is only a default; `docker build --build-arg NODE_VERSION=...` overrides it, so rewriting the `ARG` line would change the build's fallback rather than the image the stage actually uses.
- **`FROM scratch`** — the empty base image, which has no tags or digest to update.

`# shiphoist-ignore` and `--exclude` work on Dockerfiles too.

### Check a Single Image

Query available update candidates directly from the registry:

```bash
shiphoist check ghcr.io/potibm/kasseapparat:2.18.0
```

The check command is forgiving: if a tag has been deleted from the registry (404), it warns you and still resolves the latest available version instead of failing.

### Force Refresh (Bypass Cache)

Shiphoist caches registry queries locally to accelerate repeated runs. Bypass the cache with `--force`:

```bash
shiphoist --force docker-compose.yml
shiphoist check --force nginx:1.25.0
```

## Automation & CI

### Non-Interactive Updates

`--yes` applies every update within `--mode` without prompting. Without it, shiphoist needs a terminal to ask on and will refuse to run otherwise:

```bash
shiphoist --yes docker-compose.yml
```

### Limiting the Version Jump

`--mode` caps how large an update may be: `patch`, `minor` or `major` (the default). It only ever *tightens* what is applied.

Interactively, a row above the cap is still shown — just left unchecked, so you can see a bigger jump exists and select it deliberately. With `--yes` there is nobody to ask, so anything above the cap is not applied.

```bash
shiphoist --mode patch docker-compose.yml          # only patch-level jumps
shiphoist --yes --mode minor docker-compose.yml    # never a major
```

A major update is never pre-selected and never applied automatically, whatever `--mode` says.

### Previewing Without Writing

`--dry-run` reports exactly what a real run would write and leaves the file alone:

```bash
shiphoist --dry-run docker-compose.yml
# 🔍 Would apply 2 updates (dry run, nothing was written):
#
#   🚀 [web] nginx: 1.25.0 -> 1.31.6 (minor)
```

### Machine-Readable Output

`--json` writes a single report document to stdout and moves all progress and prose to stderr, so the output stays pipeable:

```bash
shiphoist --yes --json docker-compose.yml | jq -r '.updates[] | "\(.service_name) \(.old_tag) -> \(.new_tag)"'
```

`written` and `dry_run` tell you whether the file was touched; `failures` lists every image that could not be checked; `checked` versus `references` exposes how much deduplication happened.

### Skipping Images

Two ways to leave an image alone. Anything skipped is reported, so a run never
quietly covers less of the file than it appears to.

**Inline, for a specific image.** Add a `# shiphoist-ignore` comment to the line:

```yaml
services:
  web:
    image: nginx:1.25.0
  pinned:
    image: postgres:16.2 # shiphoist-ignore pinned by policy
```

The directive has to be the first thing in the comment, so a mention is not a
directive — `# TODO shiphoist-ignore this later` leaves the image updatable. A
reason may follow the directive.

**By pattern, for a whole set.** `--exclude` takes a regular expression matched
against the image repository, never the tag:

```bash
shiphoist --exclude '^ghcr\.io/mymono/' docker-compose.yml
shiphoist --exclude '^(nginx|redis)$' --yes docker-compose.yml
```

Both can be combined. Skipped references appear in the report and, for a human
run, after the summary:

```
🚫 Not checked (2):
   • postgres (line 5): ignore-directive
   • ghcr.io/potibm/billedapparat (line 7): excluded
```

### Quiet Mode

`--quiet` suppresses the progress bar, the banner, the summary and the skipped-images block. The result is still printed, because that is the point of the run. Combine it with `--json` for machine-only output.

### Exit Codes

| Situation | Exit code |
| --- | --- |
| Run completed, every image resolved | `0` |
| Some images could not be resolved, interactive run | `0` — reported, not fatal |
| Some images could not be resolved, with `--yes` or `--json` | `1` |
| Bad usage, unreadable file, cancelled run | `1` |

An unresolvable image is never allowed to abort a run: healthy images are still updated and the rest are listed under `failures`. Under `--yes` and `--json` it additionally fails the process, which is what makes those modes usable as a CI gate.

### Progress Output

On a terminal, registry checks render as a single updating line:

```
[████████████░░░░░░░░░░░░░]  8/18  ghcr.io/potibm/billedapparat
```

When output is piped or redirected, no progress is drawn at all — only a summary, so CI logs stay free of escape sequences:

```
✅ Checked 18 unique images (25 references) in 4.2s
```

Use `--verbose` for one line per image, which is useful when debugging a registry failure:

```bash
shiphoist --verbose docker-compose.yml
```

Images that could not be checked are listed after the summary:

```
⚠️  Skipped 1 image:
   • quay.io/oauth2-proxy/oauth2-proxy: unauthorized
```

## Smart Versioning Logic

Shiphoist handles complex tag topologies across registries:

- **Conservative Updates:** Patches and minor versions are prioritized. Major updates are flagged and require explicit user opt-in.
- **Precision as a Preference:** A tag is first matched against tags of the same shape, so a pinned `1.2.3` is never handed a longer tag. A channel tag such as `22-alpine` or `8.8` widens to the newest release in its major when nothing newer shares its shape, so it tracks its line instead of reporting "Up to date" forever.
- **Suffix & Flavor Preservation:** Preserves flavor variants (e.g., `-alpine`, `-fpm-bullseye`) across updates.
- **JEP-223 Support:** Correctly parses Java/Temurin tags like `17.0.6_10-jre` and finds newer builds.
- **Build ID Awareness:** Identifies incremental builds (e.g., `linuxserver/*` with `-ls212`, `bitnami/*` with `-r10`).
- **CalVer Boundary:** Differentiates between CalVer (year-based, e.g., `2024.12.16`) and SemVer to prevent false-positive major bumps.
- **Successor Rule:** When a pinpoint tag is removed upstream (e.g., `ubuntu:22.04.2`), suggests the active channel tag (`22.04`).
- **Digest Pinning for Floating Tags:** Pins tags like `latest`, `main`, or `stable` to their current SHA256 digest without altering the tag itself.
- **Tolerant 404s:** A tag that no longer exists upstream is reported and the nearest compatible candidate is offered, rather than aborting the run.

## Development

```bash
mise run test                      # run the test suite
mise run lint                      # run golangci-lint
mise run dev docker-compose.yml    # run against a file
```

The unit test suite makes no network calls. Live-registry smoke tests live in
`internal/registry/fetcher_integration_test.go` and are skipped by default:

```bash
go test -short ./...           # offline (default)
go test ./... -run Integration # opt in to live registry calls
go test -race ./...            # exercises the concurrent fetch fan-out
```

## Roadmap

Upcoming milestones and planned capabilities:

- [ ] Opt out of digest pinning with `--style preserve`
- [ ] Structured logging (`--debug`)

For the full list of planned features, performance enhancements, and technical debt items, see [TODO.md](TODO.md).

## License

MIT
