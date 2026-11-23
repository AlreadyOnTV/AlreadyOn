# Playback

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

Tuning selects the current persisted airing. Delivery uses direct play, remux or bounded transcoding according to browser support; GPU availability alone proves neither codec nor HDR compatibility.

Clients show "Tuning" until playback begins. [Startup readiness](BACKGROUND_JOBS.md) gates playback during benchmarking.

WebUI checks hls.js's media-source constructor and native HLS fallback.

## WebUI volume

WebUI remembers each profile's volume per browser/server across channels and visits. Profile IDs are unique across accounts. Clearing site data resets volume; unavailable storage limits persistence to this visit.

Guide loads/profile switches start muted. The mute button, `m` or fullscreen entry unmutes at the saved level. Only slider adjustments save volume, including explicit zero. Muting, autoplay fallback and stream changes never overwrite it.

## Touch browsers

Tablets use a large portrait player and compact landscape player; fullscreen is explicit and exits on portrait rotation. Phones use the large guide and native fullscreen. Exit restores the guide; portrait re-enables entry. Shared orientation prevents viewport-triggered cancellation. Native video fullscreen or viewport fallback handles unsupported/denied fullscreen. Orientation remains unlocked; desktop is unchanged.

## Session contract

Tunes require scoped playback credentials. Never expose management keys. Session status and leases govern activity and cleanup. Releasing a revoked token is idempotent; live tokens require their principal/channel. [OpenAPI](../api/openapi.yaml) defines fields and errors.

Dashboard time shows `HH:MM/HH:MM` position/duration through commercials, or `Unavailable`.

Schedule times are UTC. Presentation delays four seconds; transition preparation may start up to eight seconds before a boundary. Clients must respect server timing and retry hints, cancel obsolete work after channel changes and handle expired sessions without retry storms.

## Auto quality

| Device | Minimum rung | Starting rung | Maximum rung |
| --- | ---: | ---: | ---: |
| Android phone or tablet | 240p | 480p | 720p |
| Web browser | 360p | 720p | 1080p |
| Android TV | 360p | 720p | 1080p, or 2160p with compatible display, decoder and server |

Auto video conversions share a quality ceiling. When admission runs out of room or a player needs a lower rung after repeated buffering or frame drops, existing and new viewers step toward a lower common rung. Players below it can move up after roughly 30 seconds of healthy playback, or a minute after a downshift. Brief dips in the live buffer do not trigger a downshift. A viewer leaving clears the temporary ceiling so the remaining viewers can probe upward. They also retry after a one-minute settling window. Source, device, connection and measured pipeline limits still bound each stream. A connection below the minimum can still buffer.

Server logs record Auto tune outcomes and shared-ceiling changes. Updated players report the reason for a quality change and the buffer and frame sample that prompted it.

Quick calibration checks two workers at the starting rung. Full calibration also measures intermediate SD or HD output alongside the lowest and highest rungs.

## Processing

FFmpeg work has bounded lifetimes and cooperative resource controls. Foreground reservations can pause governed background work for the specific high-cost HDR conversion path. Preserve cancellation, stream limits and process cleanup when changing delivery.

MPEG-TS HLS carries AAC through ADTS. Converted 7.1 sources use 5.1 AAC because
FFmpeg rejects eight-channel ADTS; direct play can retain supported layouts.

At airing transitions, a conversion without a matching capacity sample may use the conservative single-rendition fallback only while aggregate CPU, memory and transcode-slot budgets allow it. A measured-unsustainable pipeline remains blocked. Matching hardware SDR measurements select a sustainable output before admission, including lower-quality fallback; HDR still requires its matching pipeline matrix.

Keep media paths within configured roots and library mounts read-only. Authorize resources and reject unsafe inputs.

## Validation

Use synthetic fixtures for channel changes, seeking, program transitions, session expiry, concurrent viewers and failure recovery. Check direct, remux and transcode paths separately. Hardware compatibility requires separate evidence.

See [scheduling](SCHEDULING.md), [TLS](TLS.md) and [runtime tests](../tests/integration/README.md). HTTPS tests cover discovery; proxies remain outside pairing.

See [channel navigation](CHANNEL_NAVIGATION.md) for wraparound controls and edge cases.

Brightness and text sizing use device controls, browser zoom and system accessibility settings. Current clients ignore legacy nullable `fontLevel` and `brightnessLevel` profile fields; the API retains them for older clients without deleting saved preferences. Android typography respects system font scaling.

FFmpeg processes reopen validated media pathnames. Read-only mounts and trusted writers remain necessary: root-confined reads do not eliminate that subprocess pathname race.

See [transcoding](VIDEO_PROCESSING.md).
