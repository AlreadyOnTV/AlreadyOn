# Channel navigation

Channel surfing follows the current visible guide order and skips channels with no scheduled programs. Moving forward past the last playable channel wraps to the first; moving backward past the first wraps to the last. A single playable channel stays tuned without restarting its playback. Empty lists do nothing.

WebUI fullscreen arrow navigation wraps while tuning. Guide Up/Down navigation wraps the browsing cursor without tuning until the viewer selects a program. Phone fullscreen swipes retain their existing directions and controls. TV fullscreen channel controls use the same shared selection policy; TV guide Up/Down wraps row focus and scrolls the destination into view before focusing it.

Channel changes retain existing player settings and presentation. This feature does not introduce new controls or change HUD navigation. Use Back to leave the TV guide.

Run `node --test web/tests/channelNavigation.test.ts` for browser policy regressions. Android unit tests cover both directions, visible ordering, empty rows and single-channel/stale-selection cases. Device and fullscreen playback acceptance remain separate from policy tests.
