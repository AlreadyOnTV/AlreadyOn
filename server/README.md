# Server

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

The Go server provides authentication, catalog scanning, scheduling, administration and scoped playback delivery. SQLite persists installation state; FFmpeg and FFprobe provide media inspection and processing.

Run commands from this directory: `go test ./...` for unit tests and `go build ./cmd/server` for a native build. The repository's `make preview` starts the coordinated browser/server development preview.

Keep migrations versioned and preserve existing databases. Confine filesystem access to configured roots and bound background work, subprocesses and request lifetimes.

## Transcoding capacity

Outputs are H.264/yuv420p at 1080p60/12 Mbps, 720p30/5 Mbps, 480p30/2.5 Mbps or 360p30/1.5 Mbps. AAC audio is budgeted separately; benchmark output uses 192 kbps stereo. Planning preserves aspect ratio, never upscales and chooses a profile reported by the client.

The idle-only background matrix uses deterministic test patterns, fixed noise seed and silent AAC: 3840x2160p60 HEVC Main 10/yuv420p10le PQ and HLG (40 Mbps), 1920x1080p30 HEVC Main/yuv420p SDR (6 Mbps), 1920x1080p60 H.264 High/yuv420p SDR (8 Mbps), and 854x480p30 H.264 Main/yuv420p SDR (1 Mbps). PQ and HLG split the primary HDR class because their tone maps differ. Fixtures have no HDR10+ dynamic metadata. Every valid no-upscale pairing measures decode, filters/tone mapping, H.264 encode and AAC encode together. Results persist by source class; automatic work yields when viewers become active. Run the full matrix on an idle host with `cd server && go test ./internal/playback -run '^$' -bench '^BenchmarkCapacityMatrix$' -benchtime=1x -count=1 -v`.

Auto process capacity is at most four resource slots. Admission combines measured source/rung speed, 80% CPU headroom, host-available memory and the configured encoder; a measured-unsustainable class is rejected without lowering capacity for other classes. Unmatched single-rendition pipelines use an optimistic 1.3x speed estimate but still share aggregate CPU, memory and four-slot budgets with measured streams; unmeasured ladders are denied. A two-rendition ladder costs two slots. FFmpeg thread counts are worker ceilings, not CPU quotas. Auto steps down after two 10-second windows below 1.5 seconds of media buffer or above 5% dropped frames, with a 45-second cooldown and no automatic step-up. Buffer means `video.buffered.end(currentTime) - currentTime`; two-second segments, four-segment live sync and an eight-second presentation delay target five seconds buffered but cannot guarantee it on every network.

See [the project brief](../docs/PROJECT_BRIEF.md), [playback](../docs/PLAYBACK.md) and [OpenAPI](../api/openapi.yaml). Runtime images are built from `deploy/Dockerfile`; development account provisioning is excluded from them.

## Resource metrics

CPU, memory, disk and network samples include timestamps and units. Distinguish application from optional host-wide measurements; missing history is unavailable, not zero. Adapter selection and totals describe the actual source. A host-metrics helper has a separate permission boundary and must not grant the application broad host access. Test unavailable sources, partial history and restart behavior deterministically.

## Runtime packaging

The container keeps supported codecs, GPU libraries, certificates, time zones and license/build metadata while leaving build tools in CI. Server and local administration commands share one executable through filename aliases. Packaged-runtime tests must cover that command dispatch and temporary-account recovery as well as playback.
