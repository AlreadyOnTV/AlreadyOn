# Server

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

The Go server provides authentication, catalog scanning, scheduling, administration and scoped playback delivery. SQLite persists installation state; FFmpeg and FFprobe provide media inspection and processing.

Run commands from this directory: `go test ./...` for unit tests and `go build ./cmd/server` for a native build. The repository's `make preview` starts the coordinated browser/server development preview.

Keep migrations versioned and preserve existing databases. Confine filesystem access to configured roots and bound background work, subprocesses and request lifetimes.

See [the project brief](../docs/PROJECT_BRIEF.md), [playback](../docs/PLAYBACK.md) and [OpenAPI](../api/openapi.yaml). Runtime images are built from `deploy/Dockerfile`; development account provisioning is excluded from them.

## Resource metrics

CPU, memory, disk and network samples include timestamps and units. Distinguish application from optional host-wide measurements; missing history is unavailable, not zero. Adapter selection and totals describe the actual source. A host-metrics helper has a separate permission boundary and must not grant the application broad host access. Test unavailable sources, partial history and restart behavior deterministically.

## Runtime packaging

The container keeps supported codecs, GPU libraries, certificates, time zones and license/build metadata while leaving build tools in CI. Server and local administration commands share one executable through filename aliases. Packaged-runtime tests must cover that command dispatch and temporary-account recovery as well as playback.
