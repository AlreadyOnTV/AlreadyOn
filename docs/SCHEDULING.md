# Scheduling

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

Channels contain persisted UTC airings generated from the normalized library and filter settings. Published airings stay stable: library additions or metadata refreshes extend future coverage rather than rewriting the current program.

Rotation is deterministic for a channel's existing state. Repeat limits, duration accounting and commercial placement must produce valid, non-overlapping entries. Changes to generation should be tested with a fixed clock and small synthetic catalogs.

All metadata configurations use the same audience-focused defaults. TVmaze/Wikidata fill gaps after optional TMDb; manual corrections win. Free sources supply genres/dates, not age certification. Unknown or ambiguous audiences stay unrestricted until manually validated. Legacy broad free mixes are hidden without deleting their published schedules.

Channel filter previews and applied schedules must use the same selection semantics. Document deliberate contract changes in [OpenAPI](../api/openapi.yaml) and add focused regression cases.

Do not rebuild published schedules to hide a test failure. Inspect persisted entries, generation inputs and clock handling. Live library investigation must remain non-destructive.

Guides retain real ended airings overlapping the current half-hour column. The API supplies these separately as `previousPrograms`; clients clip them to the viewport without changing original timestamps or real gaps. Ended boxes are grey and do not tune. Text is shown only when the complete time span fits; titles and episode labels truncate to one line. Current/upcoming navigation remains separate.

## Channel filters

Preview and apply share selection semantics. Combine inclusion/exclusion rules explicitly and consistently handle missing metadata, excluded folders and unavailable files. Test empty/overlapping selections and provider combinations and manual inheritance with synthetic catalogs. Coverage extends future programming without rewriting published airings or rotation state.

Date Range defaults are All, Modern (2010 onward), and Classics (through 2009). Custom ranges remain supported. The upgrade removes untouched, unused decade presets; edited or channel-referenced ranges are preserved. API keys `era` and `eras` retain their existing names for compatibility.

Filename identification recognizes ordinary seasons and explicit year-based seasons from 1900–2099 (`S1940E01` and `Season 1940` folders). Episode markers take precedence over parenthesized movie years. Alternate `1x02` seasons remain limited to two digits to avoid interpreting resolutions such as `1920x800` as TV. A normal library rescan repairs prior automatic movie guesses for unchanged year-season episodes without re-probing their media. Type changes clear incompatible provider identities, dates and automatic classification so metadata can refresh as TV; manual identity corrections and independent manual genre, rating and audience overrides remain protected. Existing file/item IDs and published airings are preserved.

The WebUI guide’s top-left header cell shows the current device-local time using the profile’s time format. It updates on each minute boundary and refreshes when the tab becomes visible or regains focus, independently of guide-data polling.
