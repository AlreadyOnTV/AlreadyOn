# Project brief

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

AlreadyOn schedules a server owner’s media library into channels with a program guide and continuous playback.

## Architecture

- **Server:** Go HTTP API, SQLite persistence and background library/metadata jobs.
- **Web:** React and TypeScript guide, player and administration interface.
- **Media:** FFmpeg/FFprobe inspect and process files; delivery selects direct playback, remuxing or transcoding according to client capabilities.
- **Clients:** Android phone/tablet and Google TV share the native model and player. Apple clients are not planned.

Dashboard disk readings are container-scoped; each mount shows bandwidth, IOPS, latency, capacity, or `N/A`.

Flow: scan → catalog → filters → schedule → playback. [OpenAPI](../api/openapi.yaml) defines the API.

## Invariants

Published airings remain stable. Adding media extends future schedules rather than rewriting what users are watching. Store schedule times in UTC and render them in the chosen timezone.

Owner and viewer permissions are enforced by the server. Profiles belong to accounts. Media requests use scoped credentials; management credentials must not travel to external receivers.

Keep library paths confined to configured roots. Preserve live configuration and databases. Synthetic tests must use isolated storage.

## Product boundaries

Free TVmaze/Wikidata metadata is automatic; optional TMDb supplies richer metadata. All sources use one channel catalog; do not pretend unsupported metadata is known. Missing or invalid ratings default to unrestricted and remain eligible for unrestricted channels.

[Playback](PLAYBACK.md), [scheduling](SCHEDULING.md), [TLS](TLS.md) and [channel filters](SCHEDULING.md#channel-filters) explain subsystem contracts. [Delivery](CI_CD.md) defines validation and release gates.

Keep changes focused and demonstrate user-visible behavior with tests.

## Built-in cable lineup

Channels 101–138 run from kids (101–106), through teens (107–120), to unrestricted TV (121–132) and movies (133–136), plus teen comedy (137–138). Names are at most 20 characters. Kids have animated and live-action TV plus movies; teens have cartoons, anime, reality, TV mixes, fantasy/sci-fi, action and movies. Anime uses its certification to determine audience. Teen/adult cartoon channels exclude anime.

Classics ends in 2009; Modern starts in 2010, using TV premiere dates. Unknown years use Modern. Teen anime and unrestricted genres each have two independently scheduled channels from the same pool, with no date range split. Teen and unrestricted TV mixes accept all genres, ensuring titles without theme metadata have a destination. Family channels are omitted. Empty channels auto-hide. Metadata edits and the audience upgrade preserve published schedules. Apply Channels explicitly rebuilds lineups using the saved corrections.

Audience identity is separate from cumulative viewing permission. Automatic G/PG movies and TV-Y/TV-Y7/TV-Y7-FV/TV-G shows belong to Kids; PG-13 movies and TV-PG/TV-14 shows belong to Teens. R/NC-17/TV-MA and missing, invalid or unrated certifications default to unrestricted. Genres alone do not raise audience. Automatic classifications need no review. Manual validation can approve an exception while retaining the official rating; TV-MA/R/NC-17 remain unrestricted. Defaults match audience exactly, preventing children's programs from filling teen/adult channels. Upgrades preserve saved filters and overrides; owners may explicitly reset them.

The Media library lists identified, non-missing movies and grouped shows, including unprobed files. New episodes inherit show corrections; episode overrides take precedence. Returning an episode audience to automatic restores show inheritance. Unidentified files appear after identification.
