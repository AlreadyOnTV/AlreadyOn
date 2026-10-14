# API compatibility

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

[OpenAPI](openapi.yaml) is the source of truth for endpoint paths, schemas and errors. This pre-1.0 API permits additive fields; handle documented failures. Breaking changes require review and migration notes.

## Authentication

Browser sessions use cookies. Management API keys carry owner authority. Playback credentials are scoped to media delivery and must not be treated as management keys. Enforce account/profile ownership on every protected operation.

TV pairing adds local HTTPS discovery/trust metadata, PIN login and per-account PIN management. PIN login creates the same account session used by other clients; it does not bypass profile ownership, disabled accounts, password-change restrictions or stream limits. PINs must be unique within an instance. See [Google TV](../docs/GOOGLE_TV.md).

## Playback

Use the tune endpoint to obtain an authorized session, then follow playback status, lease and timing fields. Honor retry hints and server time; do not construct media URLs or assume codec support. Expired credentials require a fresh authorized flow.

## Configuration

A per-channel commercial setting of `enabled: null` inherits the server default. An empty folder selection means all configured commercial folders. Clients must preserve these distinctions rather than substituting convenient defaults.

Server details expose authenticated server identity and version. Removed routes must not return unrelated data. Check OpenAPI for historical endpoints.

## Changes

Update schema, implementation, consumers and tests together. Document visible changes; never include credentials or private transcripts.

OpenAPI declares operation roles.

Channel filters accept `excludedGenres` (exclusions win) and `requireKnownYear`. These optional fields default to no exclusions and allowing unknown years. Restricted built-in defaults enable known-year filtering.

Native account PIN sign-in now requires both `username` and `pin` in `POST /api/auth/tv-login`. A PIN supplied with another account’s username is rejected. Phone/tablet and TV use this shared LAN-only flow after discovery or a 30-second manual-address fallback.

Native PIN sign-in now issues a persistent, revocable session; browser expiry settings still apply to browser sign-in. Valid native sessions migrate to persistence; expired/revoked sessions are never restored. Auth status refreshes migrated native cookies.

The temporary `DELETE /api/testing/commercial` operation has been removed. Playback no longer offers deletion of commercial library files.

Authenticated owner and viewer accounts can read `/api/server/details`, `/api/server/activity` and `/api/server/metrics` for native Dashboards. Management writes remain owner-only. Server details add `serverName`; the existing `appVersion` JSON field continues to identify the server build for compatibility, while UI labels call it Server Version. Activity is server-wide, including other profiles, source IPs and background filenames.

See [Subtitles](../docs/SUBTITLES.md) for profile-mode compatibility.
