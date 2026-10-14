## Improvements

- Keep the selected profile avatar/name together on one line on WebUI, phone and Google TV, including TV settings.
- Simplify profile menus to Switch Profile, Profile, Settings (WebUI Admin profiles), Server (WebUI owner accounts), then Sign Out. Native menus show Switch Profile, Profile and Sign Out.
- Move Dashboard to the first/default tab inside WebUI Server and rename Accounts to Home Users; existing bookmarks keep working with existing owner permission checks.
- Show device name/type, observed IP, last activity and expiry alongside per-device sign-out in account settings. Phone-width tables scroll within their own accessible region.

The session API adds descriptive device metadata and server-observed peer IPs. The additive migration preserves existing sessions and expiry; forwarding headers cannot override the stored address. See [profile and session contracts](../PROFILE_MENU.md).
