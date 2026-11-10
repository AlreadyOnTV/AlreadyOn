# Playback

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

Tuning selects the current persisted airing. Delivery uses direct play, remux or bounded transcoding according to browser support; GPU availability alone proves neither codec nor HDR compatibility.

Web, phone and TV show "Tuning" until playback begins.

WebUI capability checks use hls.js's selected media-source constructor, including Apple's `ManagedMediaSource`. Without MSE, native HLS remains available when supported.

## Touch browsers

Tablets use the large guide player in portrait and compact player in landscape; fullscreen is explicit and exits on portrait rotation. Phones keep the large guide, fill landscape and request native fullscreen when allowed. Exit restores the guide; portrait rotation re-enables entry. Shared orientation prevents viewport changes cancelling fullscreen. Native video fullscreen or a viewport fallback covers unsupported or denied stage fullscreen. Orientation stays unlocked; desktop behavior is unchanged.

## Session contract

Tunes require scoped playback credentials. Never expose management keys in manifests, browser logs or receiver requests. Session status and leases govern activity and cleanup. [OpenAPI](../api/openapi.yaml) defines fields and errors.

Dashboard Playback Time shows program position/full duration as `HH:MM/HH:MM`, retained during commercials, or `Unavailable`.

Schedule times are UTC. Presentation uses a four-second delay; transition preparation may start up to eight seconds before a boundary. Clients must respect server timing and retry hints, cancel obsolete work after channel changes and handle expired sessions without retry storms.

## Processing

FFmpeg work has bounded lifetimes and cooperative resource controls. Foreground reservations can pause governed background work for the specific high-cost HDR conversion path. These controls are sampled policies, not operating-system hard caps. Preserve cancellation, stream limits and process cleanup when changing delivery.

At airing transitions, a conversion without a matching capacity sample may use the conservative single-rendition fallback only while aggregate CPU, memory and transcode-slot budgets allow it. A measured-unsustainable pipeline remains blocked.

Media paths must stay within configured roots. Serve only authorized resources and reject unsupported or unsafe inputs. Keep read-only mounts for normal library playback.

## Validation

Browser sign-in checks identify setup and login by the dialog’s accessible name so branded layouts do not require a heading. Admin layout checks include account TV PIN management before profiles and browser sessions. Use synthetic fixtures for channel changes, seeking, program transitions, session expiry, concurrent viewers and failure recovery. Check direct, remux and transcode paths separately. Hardware and physical-device compatibility need their own evidence; browser automation cannot certify every GPU or television.

See [scheduling](SCHEDULING.md), [TLS](TLS.md) and [runtime tests](../tests/integration/README.md).

Browser settings checks verify friendly server-name persistence through the settings API. Direct HTTPS API tests cover native discovery; proxied requests remain outside the LAN pairing boundary.

See [channel navigation](CHANNEL_NAVIGATION.md) for wraparound controls and edge cases.

Brightness and text sizing use device controls, browser zoom and system accessibility settings. Current clients ignore legacy nullable `fontLevel` and `brightnessLevel` profile fields; the API retains them for older clients without deleting saved preferences. Android typography respects system font scaling.

External FFmpeg processes reopen validated media pathnames. Read-only media mounts and trusted host writers remain necessary because root-confined direct reads do not eliminate that subprocess pathname race.
