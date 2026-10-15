# Profile menu

All clients show avatar and name as one top-right control. Long names truncate; WebUI retains an accessible name and tooltip. It stays visible at compact widths.

Menu order is Switch Profile, Profile, Settings, Server, Sign Out. Switch Profile, Profile and Sign Out appear for every signed-in profile. Settings appears only for Admin profiles in WebUI. Server appears only for owner accounts in WebUI. Native menus contain Switch Profile, Profile and Sign Out. TV adds Exit App below Sign Out, retaining the session.

WebUI keyboard Enter/Space opens the menu; Escape or clicking outside closes it. Switching profiles retains profile PIN checks and returns to the Guide. TV opens the menu with Switch Profile focused.

Dashboard is the first/default tab inside Server, followed by Home Users, Media, Commercials, Channels, Server, Logs and API. `/admin/accounts` selects Home Users; `/dashboard` redirects to `/admin/dashboard`. Viewers return to the Guide. Monitoring authorization is unchanged.

See [profile creation](PROFILE_CREATION.md) for the WebUI Admin-only Add Profile shortcut and sorted switch list.

## Sessions

Settings shows Device, IP Address, Last Active, Expires and Sign Out in the Sessions table. The current session stays first, with a noninteractive blue This Session pill replacing Sign Out; others sort by recent activity. Wide tables scroll within a keyboard-accessible region at narrow widths.

Web browsers do not expose a reliable computer hostname. Device labels use Windows, Linux or Mac; native clients use Google TV or Android Phone. Type is omitted from the table; the API retains its compatibility field. IP addresses come from the socket peer, never untrusted forwarding headers.

Day fields save on blur independently of Save Changes. Successful saves show a green checkmark in reserved space for five seconds. Toggles apply immediately.

Owner-only **Remove Inactive Sessions** defaults to On and 14 days. It applies to existing and future sessions even with Never Expire enabled. Activity includes authenticated API and media requests; missing historical activity falls back to creation time. Sessions idle longer than the threshold are invalid immediately and removed on startup, hourly, when listing sessions, or after policy changes. Turning cleanup off does not restore removed sessions.


See [saved API credential entry](SAVED_CREDENTIALS.md) for draft-only visibility controls.



Active WebUI, phone and TV clients refresh shared profile settings and avatars every five minutes and when visible/resumed. Native Sign Out retains the encrypted session for recent-server reconnection; WebUI session revocation remains authoritative. Profile PINs and local drafts remain independent.
