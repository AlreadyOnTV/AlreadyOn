# Playback

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

Tuning selects the current persisted airing. Delivery uses direct play, remux or bounded transcoding according to client capabilities. The browser checks hls.js and native HLS. Clients show “Tuning” until playback begins. [Startup readiness](BACKGROUND_JOBS.md) gates playback during benchmarking.

## Controls

WebUI saves manual volume adjustments per profile and browser, including zero. Guide loads and profile switches start muted. The mute button, `m` or fullscreen entry unmutes at the saved level. Clearing site data resets volume.

Tablets use a large portrait player and compact landscape player. Fullscreen requires an explicit action and exits on portrait rotation. Phones use the large guide and native fullscreen or a viewport fallback. Exiting restores the guide. Orientation stays unlocked.

Device brightness and accessibility settings control display and typography. Current clients ignore the legacy `fontLevel` and `brightnessLevel` API fields. See [channel navigation](CHANNEL_NAVIGATION.md).

## Sessions

Tunes require scoped playback credentials. Status and leases govern activity and cleanup. Token revocation is idempotent and scoped to the principal and channel. The Dashboard shows elapsed and total program time through commercials when available. See [OpenAPI](../api/openapi.yaml).

Schedule times are UTC. Playback presents eight seconds behind the server clock. Clients may prepare the next airing up to eight seconds early. Respect retry hints and cancel obsolete work on channel changes. See [scheduling](SCHEDULING.md).

## Auto quality

| Device | Minimum | Start | Maximum |
| --- | ---: | ---: | ---: |
| Android phone or tablet | 240p | 480p | 720p |
| Browser | 360p | 720p | 1080p |
| Android TV | 360p | 720p | 1080p, or 2160p with compatible display, decoder and server |

Auto conversions share a ceiling. Admission pressure, repeated buffering or dropped frames can lower it. Healthy players can upgrade after about 30 seconds, or a minute after a downshift. A departure or a one-minute settling window clears temporary pressure. Source, device, connection and measured pipeline limits still apply.

Server logs show tune outcomes, ceiling changes and the client’s reported buffer and frame sample. See [transcoding](VIDEO_PROCESSING.md) for Quick and Full calibration.

## Processing and validation

FFmpeg uses bounded workers and can pause competing background work for expensive HDR conversions. MPEG-TS HLS converts 7.1 audio to 5.1 AAC. Direct play can preserve supported layouts.

At airing boundaries, unmeasured conversions can use the single-rendition fallback within CPU, memory and slot budgets. Measured-unsustainable pipelines are blocked. HDR requires a matching conversion pipeline.

Keep media mounts read-only and restrict writers. FFmpeg reopens validated paths, so root confinement alone cannot eliminate pathname races.

Use synthetic fixtures to test tune, seek, transitions, lease cleanup and concurrent viewers. Validate direct, remux and transcode separately, with hardware checks for GPU paths. See [runtime tests](../tests/integration/README.md) and [TLS](TLS.md).
