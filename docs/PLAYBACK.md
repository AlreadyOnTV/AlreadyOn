# Playback

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

Tuning selects the current persisted airing. Delivery uses direct play, remux or bounded transcoding according to browser support; GPU availability alone proves neither codec nor HDR compatibility.

Clients show "Tuning" until playback begins. [Startup readiness](BACKGROUND_JOBS.md) gates playback during benchmarking.

WebUI checks hls.js's media-source constructor, including Apple's `ManagedMediaSource`, with native HLS fallback where supported.

## WebUI volume

WebUI remembers each profile's volume per browser/server across channels and visits. Profile IDs are unique across accounts. Clearing site data resets volume; unavailable storage limits persistence to this visit.

Guide loads/profile switches start muted. The mute button, `m` or fullscreen entry unmutes at the saved level. Only slider adjustments save volume, including explicit zero. Muting, autoplay fallback and stream changes never overwrite it.

## Touch browsers

Tablets use a large portrait player and compact landscape player; fullscreen is explicit and exits on portrait rotation. Phones use the large guide, filling landscape and requesting native fullscreen. Exit restores the guide; portrait re-enables entry. Shared orientation prevents viewport-triggered cancellation. Native video fullscreen or viewport fallback handles unsupported/denied stage fullscreen. Orientation remains unlocked; desktop behavior is unchanged.

## Session contract

Tunes require scoped playback credentials. Never expose management keys in manifests, browser logs or receiver requests. Session status and leases govern activity and cleanup. Releasing an already-revoked token is idempotent; live tokens still require their owning principal/channel. [OpenAPI](../api/openapi.yaml) defines fields and errors.

Dashboard time shows `HH:MM/HH:MM` position/duration through commercials, or `Unavailable`.

Schedule times are UTC. Presentation delays four seconds; transition preparation may start up to eight seconds before a boundary. Clients must respect server timing and retry hints, cancel obsolete work after channel changes and handle expired sessions without retry storms.

## Processing

FFmpeg work has bounded lifetimes and cooperative resource controls. Foreground reservations can pause governed background work for the specific high-cost HDR conversion path. Preserve cancellation, stream limits and process cleanup when changing delivery.

At airing transitions, a conversion without a matching capacity sample may use the conservative single-rendition fallback only while aggregate CPU, memory and transcode-slot budgets allow it. A measured-unsustainable pipeline remains blocked. Matching hardware SDR measurements select a sustainable output before admission, including lower-quality fallback; HDR still requires its matching pipeline matrix.

Keep media paths within configured roots and library mounts read-only. Authorize resources and reject unsafe inputs.

## Validation

Use synthetic fixtures for channel changes, seeking, program transitions, session expiry, concurrent viewers and failure recovery. Check direct, remux and transcode paths separately. Hardware compatibility requires separate evidence.

See [scheduling](SCHEDULING.md), [TLS](TLS.md) and [runtime tests](../tests/integration/README.md).

Direct HTTPS tests cover discovery; proxied requests remain outside LAN pairing.

See [channel navigation](CHANNEL_NAVIGATION.md) for wraparound controls and edge cases.

Brightness and text sizing use device controls, browser zoom and system accessibility settings. Current clients ignore legacy nullable `fontLevel` and `brightnessLevel` profile fields; the API retains them for older clients without deleting saved preferences. Android typography respects system font scaling.

FFmpeg processes reopen validated media pathnames. Read-only mounts and trusted writers remain necessary: root-confined reads do not eliminate that subprocess pathname race.
