# Profile menu

All clients combine avatar/name top-right, visible at compact widths. Long names truncate; WebUI retains accessible names/tooltips.

Menu order: Switch Profile, Profile, Settings (Admin profiles), Sign Out. WebUI adds Server (owner accounts) before Sign Out; native clients omit it. TV adds Exit App after Sign Out, retaining sessions.

WebUI Enter/Space opens; Escape/outside clicks close. Switching retains PIN checks and returns to Guide. TV opens focused on Switch Profile.

Server tabs: Dashboard (default), Home Users, Media, Commercials, Channels, Server, Logs, API. `/admin/accounts` selects Home Users; `/dashboard` redirects to `/admin/dashboard`. Viewers return to Guide; monitoring authorization is unchanged.

See [profile creation](PROFILE_CREATION.md) for Admin-only Add Profile and switch-list sorting.

## Native Settings

Native Settings uses phone tabs or a TV sidebar; Back returns from controls to categories before leaving. [Release Cycle](ANDROID_UPDATES.md) is device-local and defaults to Stable.

Profiles supports account-scoped Name/Role/Audience creation/editing, confirmed deletion and PIN reset. TV confirmations focus Cancel. Failed saves retain drafts; partial updates preserve unrelated fields. Active deletion returns to the chooser; demotion exits Settings. Server account authorization and last-Admin enforcement remain unchanged. Personal preferences remain under Profile.

## Sessions

Settings lists Device, IP Address, Last Active, Expires, Sign Out. Current session comes first with a noninteractive blue This Session pill; others sort by recent activity. Narrow tables scroll in a keyboard-accessible region.

Device labels: Windows/Linux/Mac (browsers lack reliable hostnames), Google TV/Android Phone. Type remains API-only for compatibility. IPs use socket peers, never untrusted forwarding headers.

Day fields save independently on blur with five-second success checks; toggles apply immediately.

Owner-only **Remove Inactive Sessions** defaults On/14 days, including existing/future and Never Expire sessions. Authenticated API/media requests count as activity; missing history uses creation time. Overdue sessions invalidate immediately; removal runs at startup, hourly, on listing or policy changes. Disabling cleanup cannot restore sessions.

See [credential visibility](SAVED_CREDENTIALS.md) for draft-only controls.

Clients refresh shared profiles/avatars every five minutes and on visibility/resume. Native Sign Out retains encrypted sessions for reconnection; WebUI revocation remains authoritative. PINs/drafts stay independent.

The phone/tablet and TV profile chooser refreshes once when opened, without polling or a Refresh link. Shared background profile refreshes pause while the chooser is open. Reopen it to see later WebUI changes. Sign Out remains available, including when no profiles exist.

## WebUI profile autosave

WebUI stays on-page: dropdowns/icons save immediately; names save after typing pauses/blur, never blank. PINs require Save PIN with matching 4–6 digits; Cancel restores pre-edit view. Removal requires confirmation. No page-level Save Changes/Cancel; native save flows are unchanged.

Server success shows per-setting green checks for five seconds, then fades; failures show field errors. Unchanged values never write. Serialized writes coalesce queued same-setting edits, preserving newest choices/unrelated settings. Remote refreshes update untouched fields, preserving incomplete edits.

Active Streaming Sessions shows client-reported App Version, never the server version; older clients display Unknown. Web players report their bundled source version; Android reports the installed APK version. Source-test containers receive a read-only revision file when Git is unavailable. Packaged-runtime checks verify that served WebUI assets contain their source revision.
