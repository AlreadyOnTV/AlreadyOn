# Google TV client

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

The `:tv` target shares networking, profiles, guide, branding, quality, mute, audio and subtitles with mobile `:app`. TV uses fixed landscape and a Leanback launcher. It starts with a compact player above the guide. Channel selection opens fullscreen; Back restores the guide without retuning.

The chooser says **Select Profile**. Guide Up/Down changes rows; Left/Right selects programs. Channel selection opens fullscreen and unmutes.

Fullscreen Left/Right tunes with controls hidden and navigates controls when visible. Each overlay activation starts on Play/Pause. Submenus focus the current option. See [remote navigation](TV_REMOTE_NAVIGATION.md) for guide Back, mini-player and profile behavior.

## Connect

First-run WebUI setup asks for **Server Name**, username and password. Owners can rename it under **Server Settings → Server → Server Name**. Discovery reflects changes without restarting.

Accounts manage unique 6–12 digit hashed PINs under **Admin Settings → TV Pairing Pin**. PIN sessions persist until Sign Out or device revocation through **Signed-In Browsers**. Changing/removing a PIN prevents new sign-ins without revoking existing devices.

Apps scan local private IPv4 HTTPS port 19900 for 30 seconds. TV offers manual entry immediately. Bare addresses assume HTTPS and port 19900. Rescan restarts discovery. IPv6-only networks require manual entry. See [Android](ANDROID.md).

Clients trust the selected instance certificate and send username/PIN over HTTPS. Pairing requires a LAN peer and enforces account restrictions. Published TCP 19900 works without multicast. Guest isolation/firewalls can prevent discovery. See [deployment](../deploy/README.md).

## Validate

Build `./gradlew :tv:assembleDebug` in `apps/android`. For performance use `:tv:assembleDeviceReview`: optimized, non-debuggable, signed with the same debug key/identity to preserve sessions. Install its APK from `tv/build/outputs/apk/deviceReview/`.

Use a 3840×2160 Google TV AVD. Run `scripts/android/test-tv.py --serial emulator-5554 --sdk /path/to/Android/Sdk`. It clears only the selected emulator's debug app and uses disposable HTTPS/database/media fixtures on port 19900.  Physical-TV validation is still needed for LAN discovery and hardware 4K/HDR decoding.

The guide fits two hours beside the channel column at every screen width. Remote Left/Right navigation scrolls to programs outside that window. Each program shows its start and end times, a single-line title, and an episode line (`S1E2 - Episode title`) for episodic content. Text is truncated inside the program cell.

Choose Dashboard at the top of the profile menu to view server/APK details, active streams, background tasks and performance history. Every signed-in profile can use this read-only screen. Charts do not open popups; use the refresh/history controls and Back to Guide with the remote. Monitoring pauses while the app is in the background.
