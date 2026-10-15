# Google TV client

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

The `:tv` target shares networking, profiles, guide, branding, quality, mute, audio and subtitles with mobile `:app`. TV uses landscape and a Leanback launcher. A preview sits above the guide. Selection opens fullscreen; Back restores the guide.

Fullscreen Up/Down tunes channels. Left/Right reveals and navigates controls; Select activates the cursor. Menu arrows navigate without tuning. Submenus focus the current option. Guide arrows wrap; Back restores the watched airing, then opens the profile menu; a second quick Back exits. The preview has no controls. See [remote navigation](TV_REMOTE_NAVIGATION.md).

## Connect

First-run WebUI setup asks for **Server Name**, username and password. Owners can rename it under **Server Settings → Server → Server Name**. Discovery reflects changes without restarting.

Accounts manage 6–12 digit hashed PINs under **Admin Settings → TV Pairing Pin**. Native Sign Out retains the encrypted session; selecting the recent server revalidates it before skipping credentials. WebUI **Sessions** revocation, expiry, inactivity cleanup and account restrictions require fresh authentication. Profile PINs still apply. Changing the account PIN affects new sign-ins only.

Apps scan local private IPv4 HTTPS port 19900 for 30 seconds. TV offers manual entry immediately. Manual entry separates host and port; IPs suggest 19900, while an empty port uses HTTPS 443. See [connection fields](SERVER_CONNECTION.md). Rescan restarts discovery. IPv6-only networks require manual entry. See [Android](ANDROID.md).

Clients trust the selected instance certificate and send username/PIN over HTTPS. Pairing requires a LAN peer and enforces account restrictions. Published TCP 19900 works without multicast. Guest isolation/firewalls can prevent discovery. See [deployment](../deploy/README.md).

## Validate

Build `./gradlew :tv:assembleDebug` in `apps/android`. For performance use `:tv:assembleDeviceReview`: optimized, non-debuggable, signed with the same debug key/identity to preserve sessions. Install its APK from `tv/build/outputs/apk/deviceReview/`.

Use a 3840×2160 Google TV AVD. Run `scripts/android/test-tv.py --serial emulator-5554 --sdk /path/to/Android/Sdk`. It clears only the selected emulator's debug app and uses disposable HTTPS/database/media fixtures on port 19900.  Physical-TV validation is still needed for LAN discovery and hardware 4K/HDR decoding.

The guide shows a two-hour window; horizontal navigation reveals programs outside it. See [remote navigation](TV_REMOTE_NAVIGATION.md) for guide selection and focus behavior.

The profile menu offers Switch Profile, Profile, Sign Out and Exit App. Switch Profile receives initial remote focus. The header keeps avatar and name on one line. Dashboard now lives inside the owner-only Server section in WebUI; native menus do not offer server administration.
