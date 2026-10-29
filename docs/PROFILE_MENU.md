# Profile menu

All clients combine avatar/name top-right, visible at compact widths. Long names truncate; WebUI retains accessible names/tooltips.

WebUI order: Switch Profile, Profile, Settings (Admin profiles), Server (owner accounts), Sign Out. Other entries appear for every signed-in profile. Native phone/TV menus include Settings immediately above Sign Out only for Admin profiles and omit Server. TV adds Exit App after Sign Out, retaining sessions.

WebUI Enter/Space opens; Escape/outside clicks close. Switching retains PIN checks and returns to Guide. TV opens focused on Switch Profile.

Server tabs: Dashboard (default), Home Users, Media, Commercials, Channels, Server, Logs, API. `/admin/accounts` selects Home Users; `/dashboard` redirects to `/admin/dashboard`. Viewers return to Guide; monitoring authorization is unchanged.

See [profile creation](PROFILE_CREATION.md) for Admin-only Add Profile and switch-list sorting.

## Native Settings

Settings contains Updates and Profiles. [Release Cycle](ANDROID_UPDATES.md) defaults to Stable and is stored per installation. Phone uses tabs; TV uses a category sidebar with D-pad focus and Back returning from controls to categories before leaving.

Profiles lists only the signed-in account's profiles. Add/edit supports Name, Role and Audience, matching WebUI administration; edits patch only changed fields. Delete and PIN reset require confirmation, initially focused on Cancel on TV. Validation failures retain drafts. The server enforces account authorization and the last-Admin rule; native Admin gating does not change the account-session API security contract. Deleting the active profile returns to the chooser; removing its Admin role exits Settings. Personal avatar, PIN, language and playback preferences remain under Profile.

## Sessions

Settings lists Device, IP Address, Last Active, Expires, Sign Out. Current session comes first with a noninteractive blue This Session pill; others sort by recent activity. Narrow tables scroll in a keyboard-accessible region.

Device labels: Windows/Linux/Mac (browsers lack reliable hostnames), Google TV/Android Phone. Type remains API-only for compatibility. IPs use socket peers, never untrusted forwarding headers.

Day fields save on blur independently of Save Changes, showing reserved-space green checks for five seconds. Toggles apply immediately.

Owner-only **Remove Inactive Sessions** defaults On/14 days, including existing/future and Never Expire sessions. Authenticated API/media requests count as activity; missing history uses creation time. Overdue sessions invalidate immediately; removal runs at startup, hourly, on listing or policy changes. Disabling cleanup cannot restore sessions.

See [credential visibility](SAVED_CREDENTIALS.md) for draft-only controls.

Clients refresh shared profiles/avatars every five minutes and on visibility/resume. Native Sign Out retains encrypted sessions for reconnection; WebUI revocation remains authoritative. PINs/drafts stay independent.

The phone/tablet and TV profile chooser refreshes once when opened, without polling or a Refresh link. Shared background profile refreshes pause while the chooser is open. Reopen it to see later WebUI changes. Sign Out remains available, including when no profiles exist.

## WebUI profile autosave

WebUI stays on-page: dropdowns/icons save immediately; names save after typing pauses/blur, never blank. PINs require Save PIN with matching 4–6 digits; Cancel restores pre-edit view. Removal requires confirmation. No page-level Save Changes/Cancel; native save flows are unchanged.

Server success shows per-setting green checks for five seconds, then fades; failures show field errors. Unchanged values never write. Serialized writes coalesce queued same-setting edits, preserving newest choices/unrelated settings. Remote refreshes update untouched fields, preserving incomplete edits.

Active Streaming Sessions shows client-reported App Version, never the server version; older clients display Unknown. Web players report their bundled source version; Android reports the installed APK version. Source-test containers receive a read-only revision file when Git is unavailable. Packaged-runtime checks verify that served WebUI assets contain their source revision.
