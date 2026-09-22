# AGENTS.md

## Commands

```bash
# Development
mise run dev docker-compose.yml          # Run the app
mise run test                            # Run all tests
mise run lint                            # Run golangci-lint
mise run test:cover                      # Tests with coverage

# Build
mise run build:local                     # Local binary (single target)
mise run release:snapshot                # Full snapshot release
```

## Linting Constraints

Strict complexity limits enforced by golangci-lint:
- `gocognit`: max cognitive complexity **15**
- `cyclop`: max cyclomatic complexity **15**
- `golines`: max line length **120**
- `gofumpt`: strict formatting
- `mnd`: magic numbers must be constants
- `gosec`: file permissions must be 0o600 or less

Run `mise run lint` after changes. Code must pass before commit.

## Architecture

Hybrid discovery/patching approach:
1. **Discovery** (`internal/discovery`): AST parsing finds images with exact line numbers
2. **Registry** (`internal/registry`): SemVer comparison and SHA256 digest resolution
3. **Patching** (`internal/patcher`): Line-based surgical replacement preserves formatting/comments

Pipeline flow: `Discoverer → Fetcher → Prompter → Patcher` (orchestrated by `internal/engine`)

## Key Conventions

- All inline comments must be in English
- Use `core.ImageUpdate` struct to pass state through the pipeline
- Registry client uses file-based cache (`internal/registry/cache.go`)
- TUI uses `charmbracelet/huh` for interactive selection
- Major updates are not pre-selected (safety feature)

## Testing

- Tests use standard `testing` package
- Mock `RegistryClient` interface for registry tests
- Integration tests require network access for real registry calls
