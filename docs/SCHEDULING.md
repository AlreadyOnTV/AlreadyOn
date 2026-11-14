# Scheduling

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

Channels persist UTC airings from library filters. Additions and metadata refreshes extend coverage without rewriting current programming.

Test deterministic rotation, repeat limits and non-overlapping commercials with fixed clocks and synthetic catalogs.

All metadata configurations use the same audience-focused defaults. TVmaze/Wikidata fill gaps after optional TMDb; manual corrections win. Free sources supply genres/dates, not age certification. Ratings determine the automatic audience: G/PG and TV-Y/TV-Y7/TV-Y7-FV/TV-G are Kids; PG-13/TV-PG/TV-14 are Teens; R/NC-17/TV-MA and missing/invalid/unrated certifications are Unrestricted. Missing ratings do not require manual review or prevent unrestricted scheduling. Genres alone do not raise the audience. Teen and unrestricted TV Mix channels accept all genres, including absent genre metadata. Classics requires a known year; Modern also accepts unknown dates. Existing saved filters and audience overrides remain unchanged unless the owner explicitly resets them. Legacy broad free mixes are hidden without deleting their published schedules.

Document filter contracts in [OpenAPI](../api/openapi.yaml) with focused regression cases.

Investigate persisted entries, generation inputs and clocks without rebuilding published schedules or altering live libraries.

Guides retain real ended airings overlapping the current half-hour column. The API supplies these separately as `previousPrograms`; clients clip them to the viewport without changing original timestamps or real gaps. Ended boxes are grey and do not tune. Text is shown only when the complete time span fits; titles and episode labels truncate to one line. Current/upcoming navigation remains separate.

## Channel filters

Preview and apply share selection semantics. Combine inclusion/exclusion rules explicitly and consistently handle missing metadata, excluded folders and unavailable files. Test empty/overlapping selections and provider combinations and manual inheritance with synthetic catalogs. Coverage extends future programming without rewriting published airings or rotation state.

Library Paths → Customize atomically saves one folder’s movie, TV and custom channel preferences. Defaults: movies use movie channels, TV uses TV channels, commercials use both; custom channels default off. Channels outside 100–199 are custom; unfiltered channels are skipped for media. One background worker merges pending edits, cancels superseded derivation and rebuilds affected channels once. Selection changes reuse the catalog; folder-list changes supersede outdated scans. Canceled scans cannot mark unvisited files missing. Work survives restart. Progress distinguishes saved from applied; failures retain choices for retry. Airings, other paths, filters and commercial enablement/timing are preserved. Right-aligned counts show saved path selections. Cancel discards changes.

Date Range offers All, Modern (2010 onward), Classics (through 2009), and custom ranges. The upgrade removes untouched, unused decade presets; edited or channel-referenced ranges are preserved. API keys `era` and `eras` retain their existing names for compatibility.

Identification recognizes ordinary and 1900–2099 year-based seasons (`S1940E01`, `Season 1940`). Episode markers precede parenthesized movie years. Alternate `1x02` seasons remain two digits to exclude resolutions such as `1920x800`. Rescanning repairs automatic movie guesses for year-season episodes without re-probing. Type changes clear incompatible automatic identity/classification; manual corrections, file/item IDs and published airings remain intact.

The guide header shows device-local time in the profile’s format. It updates each minute and on tab visibility/focus changes, independently of guide polling.
