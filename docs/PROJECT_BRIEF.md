# Project brief

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

AlreadyOn provides scheduled television-style playback from a server owner's media library. Users browse channels and a program guide, tune into the current airing and continue through program boundaries.

## Architecture

- **Server:** Go HTTP API, SQLite persistence and background library/metadata jobs.
- **Web:** React and TypeScript guide, player and administration interface.
- **Media:** FFmpeg/FFprobe inspect and process files; delivery selects direct playback, remuxing or transcoding according to client capabilities.
- **Clients:** Android phone/tablet and Google TV share the native model and player. Apple clients are not planned.

The main flow is scan → normalized catalog → channel filters → persisted schedule → scoped playback session. [OpenAPI](../api/openapi.yaml) defines the public API; [compatibility](../api/COMPATIBILITY.md) explains changes.

## Invariants

Published airings remain stable. Adding media extends future schedules rather than rewriting what users are watching. Store schedule times in UTC and render them in the chosen timezone.

Owner and viewer permissions are enforced by the server. Profiles belong to accounts. Media requests use scoped credentials; management credentials must not travel to external receivers.

Keep library paths confined to configured roots. Preserve live configuration and databases. Synthetic tests must use isolated storage.

## Product boundaries

Metadata providers are optional. Provider availability changes channel-generation options; do not pretend unsupported metadata is known. Unknown ratings require conservative filtering. Hardware acceleration and browser codec support must be detected rather than assumed.

[Playback](PLAYBACK.md), [scheduling](SCHEDULING.md), [TLS](TLS.md) and [channel filters](SCHEDULING.md#channel-filters) explain subsystem contracts. [Delivery](CI_CD.md) defines validation and release gates.

Avoid speculative features, hidden maintenance modes and broad rewrites. Keep changes small enough to review, with tests that demonstrate user-visible behavior.

## Built-in cable lineup

Channels 101–136 run from kids (101–106), through teens (107–120), to unrestricted TV (121–132) and movies (133–136). Names are at most 20 characters. Kids have animated and live-action TV plus movies; teens have cartoons, anime, reality, drama, fantasy/sci-fi, action and movies. Cartoons exclude anime except on the young-kids animation channels.

Classic ends in 2009; Modern starts in 2010, using the series premiere date for TV. Restricted defaults require a known year. Teen anime and unrestricted genres each have two independently scheduled channels from the same pool, with no era split. Unknown years appear only on unrestricted defaults. Family channels are omitted. Empty channels auto-hide. Upgrading replaces the old reserved defaults and their schedules; media, accounts and custom channels remain intact.
