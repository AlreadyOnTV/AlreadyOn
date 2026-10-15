# Google TV client

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

The `:tv` target shares networking, profiles, guide, branding, quality, mute, audio and subtitles with mobile `:app`. TV uses landscape and a Leanback launcher. A compact preview sits above the guide. Selection opens fullscreen; Back restores the guide.

Fullscreen Up/Down tunes channels. Left/Right reveals and navigates controls; Select activates the cursor. Menu arrows navigate without tuning. Submenus focus the current option. Guide arrows wrap; Back restores the watched airing, then opens the profile menu. The preview has no controls. See [remote navigation](TV_REMOTE_NAVIGATION.md).

## Connect

First-run WebUI setup asks for **Server Name**, username and password. Owners can rename it under **Server Settings → Server → Server Name**. Discovery reflects changes without restarting.

Accounts manage 6–12 digit hashed PINs under **Admin Settings → TV Pairing Pin**. Native Sign Out retains the encrypted session; selecting the recent server revalidates it before skipping credentials. WebUI **Sessions** revocation, expiry, inactivity cleanup and account restrictions require fresh authentication. Profile PINs still apply. Changing the account PIN affects new sign-ins only.

Apps scan local private IPv4 HTTPS port 19900 for 30 seconds. TV offers manual entry immediately. Bare addresses assume HTTPS and port 19900. Rescan restarts discovery. IPv6-only networks require manual entry. See [Android](ANDROID.md).

Clients trust the selected instance certificate and send username/PIN over HTTPS. Pairing requires a LAN peer and enforces account restrictions. Published TCP 19900 works without multicast. Guest isolation/firewalls can prevent discovery. See [deployment](../deploy/README.md).

## Validate

Build `./gradlew :tv:assembleDebug` in `apps/android`. For performance use `:tv:assembleDeviceReview`: optimized, non-debuggable, signed with the same debug key/identity to preserve sessions. Install its APK from `tv/build/outputs/apk/deviceReview/`.

Use a 3840×2160 Google TV AVD. Run `scripts/android/test-tv.py --serial emulator-5554 --sdk /path/to/Android/Sdk`. It clears only the selected emulator's debug app and uses disposable HTTPS/database/media fixtures on port 19900.  Physical-TV validation is still needed for LAN discovery and hardware 4K/HDR decoding.

The guide fits two hours beside the channel column at every screen width. Remote Left/Right navigation scrolls to programs outside that window. Each program shows its start and end times, a single-line title, and an episode line (`S1E2 - Episode title`) for episodic content. Text is truncated inside the program cell.

The profile menu offers Switch Profile, Profile and Sign Out. Switch Profile receives initial remote focus. The avatar and profile name stay on one line in the header, including Profile settings. Dashboard now lives inside the owner-only Server section in WebUI; native menus do not offer server administration.
