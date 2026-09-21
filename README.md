# Shiphoist

A high-performance Go CLI tool to update Docker images in `docker-compose.yml` and `Dockerfile` files, with SHA256 digest pinning.

## Usage

### Update compose file

```bash
shiphoist docker-compose.yml
```

### Check a single image

```bash
shiphoist check ghcr.io/potibm/kasseapparat:2.18.0
```

The check command is forgiving: if a tag has been deleted from the registry (404), it will warn you and still report the latest available version instead of failing.

### Force refresh (bypass cache)

```bash
shiphoist --force docker-compose.yml
shiphoist check --force nginx:1.25.0
```

## Smart Versioning

Shiphoist handles complex versioning schemes intelligently:

- **Conservative updates**: Suggests the latest version within the same major version by default. Major version bumps are shown separately and not pre-selected.
- **Suffix preservation**: Maintains flavor variants (e.g., `-alpine`, `-fpm-bullseye`) across updates.
- **JEP-223 support**: Correctly parses Java/Temurin tags like `17.0.6_10-jre` and finds newer builds.
- **Build ID awareness**: For images with build identifiers (e.g., `linuxserver/*` with `-ls212`, `bitnami/*` with `-r10`), finds newer builds of the same version.
- **CalVer boundary**: Distinguishes between CalVer (year-based, e.g., `2021.12.16`) and SemVer (e.g., `10.8.9`) to avoid suggesting obsolete year-tags as "major updates".
- **Successor rule**: When a precise tag is deleted (e.g., `ubuntu:22.04.2`), suggests the coarser channel tag (e.g., `22.04`) as a safe update.
- **Digest pinning**: For floating tags like `latest`, `edge`, or `bullseye-slim`, pins to the current digest instead of suggesting a tag change.

## License

MIT
