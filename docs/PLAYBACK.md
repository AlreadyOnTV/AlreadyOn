# Playback

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

Tuning selects the channel's current persisted airing. Delivery chooses direct playback when the browser supports the source, remuxing when the container alone needs changing, or a bounded transcoding path. GPU availability does not imply codec, HDR or browser support.

## Session contract

The server authorizes each tune and scopes media credentials to playback. Management keys must not be exposed in manifests, browser logs or external receiver requests. Session status and leases drive activity tracking and cleanup. API fields and errors are defined in [OpenAPI](../api/openapi.yaml).

Schedule times are UTC. Presentation uses a four-second delay; transition preparation may start up to eight seconds before a boundary. Clients must respect server timing and retry hints, cancel obsolete work after channel changes and handle expired sessions without retry storms.

## Processing

FFmpeg work has bounded lifetimes and cooperative resource controls. Foreground reservations can pause governed background work for the specific high-cost HDR conversion path. These controls are sampled policies, not operating-system hard caps. Preserve cancellation, stream limits and process cleanup when changing delivery.

Media paths must stay within configured roots. Serve only authorized resources and reject unsupported or unsafe inputs. Keep read-only mounts for normal library playback.

## Validation

Browser sign-in checks identify setup and login by the dialog’s accessible name so branded layouts do not require a heading. Admin layout checks include account TV PIN management before profiles and browser sessions. Use synthetic fixtures for channel changes, seeking, program transitions, session expiry, concurrent viewers and failure recovery. Check direct, remux and transcode paths separately. Hardware and physical-device compatibility need their own evidence; browser automation cannot certify every GPU or television.

See [scheduling](SCHEDULING.md), [TLS](TLS.md) and [runtime tests](../tests/integration/README.md).

Browser settings checks verify friendly server-name persistence through the settings API. Direct HTTPS API tests cover native discovery; proxied requests remain outside the LAN pairing boundary.

See [channel navigation](CHANNEL_NAVIGATION.md) for wraparound controls and edge cases.

Use [device display settings](DISPLAY.md) for brightness and text sizing.
