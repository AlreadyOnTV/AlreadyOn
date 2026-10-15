# Scheduling

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

Channels contain persisted UTC airings generated from the normalized library and filter settings. Published airings stay stable: library additions or metadata refreshes extend future coverage rather than rewriting the current program.

Rotation is deterministic for a channel's existing state. Repeat limits, duration accounting and commercial placement must produce valid, non-overlapping entries. Changes to generation should be tested with a fixed clock and small synthetic catalogs.

Provider modes differ by available metadata. Without provider metadata, the demo group is used; optional free metadata supplies broader groups; full metadata enables richer genre and rating channels. Unknown ratings require conservative handling. Library Mix can include usable content with incomplete metadata.

Channel filter previews and applied schedules must use the same selection semantics. Document deliberate contract changes in [OpenAPI](../api/openapi.yaml) and add focused regression cases.

Do not rebuild published schedules to hide a test failure. Inspect persisted entries, generation inputs and clock handling. Live library investigation must remain non-destructive.

Guides retain real ended airings overlapping the current half-hour column. The API supplies these separately as `previousPrograms`; clients clip them to the viewport without changing original timestamps or real gaps. Ended boxes are grey and do not tune. Text is shown only when the complete time span fits; titles and episode labels truncate to one line. Current/upcoming navigation remains separate.

## Channel filters

Preview and apply share selection semantics. Combine inclusion/exclusion rules explicitly and consistently handle missing metadata, excluded folders and unavailable files. Test empty/overlapping selections and provider-mode differences with synthetic catalogs. Coverage extends future programming without rewriting published airings or rotation state.
