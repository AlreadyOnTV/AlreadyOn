# Google TV client

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

The `:tv` target shares networking, profiles, guide, branding, quality, mute, audio and subtitles with mobile `:app`. TV uses fixed landscape and a Leanback launcher. It starts with a compact player above the guide. Channel selection opens fullscreen; Back restores the guide without retuning.

The centered chooser says **Select Profile**. Guide Up/Down changes rows; Left/Right selects programs. One focus target per row and drawn program cells keep navigation light. Channel selection opens fullscreen and unmutes.

Fullscreen Left/Right tunes with controls hidden and navigates controls when visible. Each overlay activation starts on Play/Pause. Submenus focus the current option. See [remote navigation](TV_REMOTE_NAVIGATION.md) for guide Back, mini-player and profile behavior.

TV uses SurfaceView, a 16 MiB buffer target and 500 ms initial media buffering; server preparation and network transfer also affect startup.

## Connect

First-run WebUI setup asks for **Server Name**, username and password. Owners can rename it under **Server Settings → Server → Server Name**. Discovery reflects changes without restarting.

Each account manages its unique 6–12 digit numeric PIN under **Admin Settings → TV Pairing Pin**, below the password fields. Use **Save Changes** to set it and **Remove Pin** to remove it. PINs are hashed. New PIN sessions persist until Sign Out or WebUI device revocation; valid existing native sessions migrate without restoring expired/revoked sessions. Replacing/removing a PIN prevents new sign-ins; revoke existing devices through **Signed-In Browsers**.

Apps scan local private IPv4 HTTPS port 19900 for 30 seconds. TV offers manual entry immediately. The recent server appears first. Bare addresses assume HTTPS and port 19900. Server selection stops scanning; Rescan restarts it. IPv6-only networks require manual entry. See [Android](ANDROID.md).

Clients accept the selected instance certificate automatically, then send username/PIN over validated HTTPS. Pairing requires a direct LAN peer, limits attempts and rejects disabled accounts or required password changes. Standard Docker bridge networking with published TCP 19900 works; no multicast or extra discovery port is needed. Guest isolation/firewalls can prevent discovery. See [deployment](../deploy/README.md).

## Validate

Build `./gradlew :tv:assembleDebug` in `apps/android`. For performance use `:tv:assembleDeviceReview`: optimized, non-debuggable, signed with the same debug key/identity to preserve sessions. Install its APK from `tv/build/outputs/apk/deviceReview/`.

Use a 3840×2160 Google TV AVD. Run `scripts/android/test-tv.py --serial emulator-5554 --sdk /path/to/Android/Sdk`. It clears only the selected emulator's debug app and uses disposable HTTPS/database/media fixtures on port 19900.  Physical-TV validation is still needed for LAN discovery and hardware 4K/HDR decoding.

The guide fits two hours beside the channel column at every screen width. Remote Left/Right navigation scrolls to programs outside that window. Each program shows its start and end times, a single-line title, and an episode line (`S1E2 - Episode title`) for episodic content. Text is truncated inside the program cell.
