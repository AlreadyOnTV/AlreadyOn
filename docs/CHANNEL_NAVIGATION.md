# Channel navigation

Channel surfing follows the current visible guide order and skips channels with no scheduled programs. Moving forward past the last playable channel wraps to the first; moving backward past the first wraps to the last. A single playable channel stays tuned without restarting its playback. Empty lists do nothing.

WebUI large/fullscreen Up/Down tunes channels. Left/Right reveals the overlay and navigates its controls; Space/Enter activates the highlighted option. Escape closes a submenu, closes the dot menu or hides the overlay. Inside dot menus, arrows navigate options without tuning. Small-player Guide arrows browse without tuning until selection. Phone fullscreen swipes retain their directions. TV fullscreen mirrors keyboard behavior with D-pad, Select and Back. TV guide arrows wrap channels and programs, revealing the destination before focusing it.

Channel changes retain player settings. In the TV guide, Back restores the watched airing, then opens the profile menu; Back from that menu returns to the airing. Browsing resets this sequence. The TV preview has no focusable controls.

Run `node --test web/tests/channelNavigation.test.ts` for browser policy regressions. Android unit tests cover both directions, visible ordering, empty rows and single-channel/stale-selection cases. Device and fullscreen playback acceptance remain separate from policy tests.
