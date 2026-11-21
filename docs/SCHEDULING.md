# Scheduling

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

Channels persist UTC airings from library filters. Updates extend coverage without rewriting current programming.

Test with fixed clocks and synthetic catalogs.

All metadata configurations use the same audience-focused defaults. TVmaze/Wikidata fill gaps after optional TMDb; manual corrections win. Free sources supply genres/dates, not age certification. Ratings determine the automatic audience: G/PG and TV-Y/TV-Y7/TV-Y7-FV/TV-G are Kids; PG-13/TV-PG/TV-14 are Teens; R/NC-17/TV-MA and missing/invalid/unrated certifications are Unrestricted. Missing ratings do not require manual review or prevent unrestricted scheduling. Genres alone do not raise the audience. Teen and unrestricted TV Mix channels accept all genres, including absent genre metadata. Classics requires a known year; Modern also accepts unknown dates. Existing saved filters and audience overrides remain unchanged unless the owner explicitly resets them. Legacy broad free mixes are hidden without deleting their published schedules.

Guides start at the current quarter-hour and label hours and half-hours. Clients clip ended airings from `previousPrograms` to the viewport, preserving timestamps and gaps. Ended boxes are gray and untunable. Text appears only if the time range fits. Titles and episode labels truncate to one line.

## Channel filters

[Preview and apply](../api/openapi.yaml) share selection semantics. Combine inclusion/exclusion rules explicitly and consistently handle missing metadata, excluded folders and unavailable files. Test empty/overlapping selections and provider combinations and manual inheritance with synthetic catalogs. Coverage extends future programming without rewriting published airings or rotation state.

Settings saves atomically persist preferences and durable background work, returning before media or channel processing. Folder lists/customization, channel filters, commercial overrides, shared definitions and metadata providers share one worker. Pending edits merge; latest values win overlaps. Superseded derivation stops; committed unaffected work is reused. Folder/provider edits cancel obsolete discovery/enrichment. Channel edits reuse discovery; commercial edits regenerate schedules. Volume-only edits queue nothing. Work survives restart; progress/retry distinguish saved from applied. Current airings remain intact. Commercial Breaks autosaves valid controls without page reloads or Save/Cancel actions. Failures retain drafts for retry. Idle completion messages stay hidden.

Library Paths → Customize applies one path across movie, TV and custom channels. Defaults enable movies for movie channels, TV for TV channels, and commercials for both; custom defaults off. Channels outside 100–199 are custom. Unfiltered channels are skipped for media. Right-aligned counts show saved path selections. Other paths/rules are preserved; Cancel discards edits. Canceled scans cannot mark unvisited files missing.

Date Range offers All, Modern (2010 onward), Classics (through 2009), and custom ranges. The upgrade removes untouched, unused decade presets; edited or channel-referenced ranges are preserved. API keys `era` and `eras` retain their existing names for compatibility.

Identification recognizes ordinary and 1900–2099 year-based seasons (`S1940E01`, `Season 1940`). Episode markers precede parenthesized movie years. Alternate `1x02` seasons remain two digits to exclude resolutions such as `1920x800`. Rescanning repairs automatic movie guesses for year-season episodes without re-probing. Type changes clear incompatible automatic identity/classification; manual corrections, file/item IDs and published airings remain intact.

The guide header shows device-local profile time, updating each minute and on tab visibility/focus changes.
