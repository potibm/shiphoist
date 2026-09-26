# 🚢 Shiphoist

An interactive CLI tool to surgically update Docker image tags in Compose files, with SHA256 digest pinning and smart version resolution.

## Features

- **Surgical Patching:** Preserves formatting, indentations, and inline comments in your Compose files.
- **SHA256 Digest Pinning:** Immutably pins updated images to their cryptographic digest.
- **Smart SemVer Resolution:** Accurately distinguishes between patch, minor, and major version jumps while preserving image flavors (e.g. `-alpine`).
- **Interactive TUI:** Review and toggle individual updates in an aligned table before writing changes to disk. Identical updates shared across services collapse into a single row, and a second prompt lets you narrow which services to apply.
- **Live Progress Bar:** Registry checks report on one updating line, so a 25-image file no longer scrolls past.
- **Fetch Deduplication:** A repository used by eight services costs one registry round-trip, not eight.
- **Single Image Inspector:** Check registry update candidates for any container image without touching a file.
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

- [ ] Native `Dockerfile` discovery & patching
- [ ] Non-interactive CI mode (`--yes` / `--dry-run`)
- [ ] Export reports as JSON (`--json`)
- [ ] Structured logging (`--debug` / `--quiet`)

For the full list of planned features, performance enhancements, and technical debt items, see [TODO.md](TODO.md).

## License

MIT
