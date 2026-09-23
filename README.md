# 🚢 Shiphoist

An interactive CLI tool to surgically update Docker image tags in Compose files, with SHA256 digest pinning and smart version resolution.

## Features

- **Surgical Patching:** Preserves formatting, indentations, and inline comments in your Compose files.
- **SHA256 Digest Pinning:** Immutably pins updated images to their cryptographic digest.
- **Smart SemVer Resolution:** Accurately distinguishes between patch, minor, and major version jumps while preserving image flavors (e.g. `-alpine`).
- **Interactive TUI:** Review and toggle individual updates before writing changes to disk.
- **Single Image Inspector:** Check registry update candidates for any container image without touching a file.

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

## Smart Versioning Logic

Shiphoist handles complex tag topologies across registries:

- **Conservative Updates:** Patches and minor versions are prioritized. Major updates are flagged and require explicit user opt-in.
- **Suffix & Flavor Preservation:** Preserves flavor variants (e.g., `-alpine`, `-fpm-bullseye`) across updates.
- **JEP-223 Support:** Correctly parses Java/Temurin tags like `17.0.6_10-jre` and finds newer builds.
- **Build ID Awareness:** Identifies incremental builds (e.g., `linuxserver/*` with `-ls212`, `bitnami/*` with `-r10`).
- **CalVer Boundary:** Differentiates between CalVer (year-based, e.g., `2024.12.16`) and SemVer to prevent false-positive major bumps.
- **Successor Rule:** When a pinpoint tag is removed upstream (e.g., `ubuntu:22.04.2`), suggests the active channel tag (`22.04`).
- **Digest Pinning for Floating Tags:** Pins tags like `latest`, `main`, or `stable` to their current SHA256 digest without altering the tag itself.

## Roadmap

Upcoming milestones and planned capabilities:

- [ ] Native `Dockerfile` discovery & patching
- [ ] Non-interactive CI mode (`--yes` / `--dry-run`)
- [ ] Export reports as JSON (`--json`)

For the full list of planned features, performance enhancements, and technical debt items, see [TODO.md](TODO.md).

## License

MIT
