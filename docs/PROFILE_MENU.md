# Profile menu

WebUI, phone and Google TV show the selected profile’s avatar and name together in the top-right header as one clickable element on one line. Long names truncate; WebUI retains the complete accessible name and tooltip. The control remains visible at compact widths.

Menu order is Switch Profile, Profile, Settings, Server, Sign Out. Switch Profile, Profile and Sign Out appear for every signed-in profile. Settings appears only for Admin profiles in WebUI. Server appears only for owner accounts in WebUI. Native menus contain Switch Profile, Profile and Sign Out. These visibility rules do not grant additional API permissions.

WebUI keyboard Enter/Space opens the menu; Escape or clicking outside closes it. Switching profiles retains profile PIN checks and returns to the Guide. TV opens the menu with Switch Profile focused.

Dashboard is the first/default tab inside Server, followed by Home Users, Media, Commercials, Channels, Server, Logs and API. The `/admin/accounts` bookmark still selects Home Users. `/dashboard` redirects to `/admin/dashboard`; viewers are redirected to the Guide by the existing Server owner gate. Monitoring APIs retain their existing authorization.

## Signed-in devices

Settings shows Device, Type, IP Address, Last Active, Expires and Sign Out in the Signed-In Browsers table. The This Browser badge sits below the device name and sizes to its full text on one line. Wide tables scroll within a keyboard-accessible region at narrow widths.

`GET /api/auth/sessions` adds `deviceName`, `deviceType` and `ipAddress` while preserving all existing fields. Names and types are best-effort user-agent descriptions of the browser/OS or native app, not private machine hostnames or security credentials. Device types are Web Browser, Phone and TV. Browser sessions use Web Browser on every operating system; native Android mobile uses Phone and native Google TV uses TV. The This Browser badge appears below the current session’s device name.

Migration 0085 adds an empty IP field to existing sessions without changing cookies, users or expiry. Sign-in and authenticated requests record the current server-observed socket peer IP. Untrusted forwarding headers are ignored; reverse-proxy deployments show the observed proxy address. Old sessions say Not recorded yet until they next connect. Last Active and Expires retain existing behavior, and session listing/revocation remain scoped to the signed-in account.


See [saved API credential entry](SAVED_CREDENTIALS.md) for draft-only visibility controls.
