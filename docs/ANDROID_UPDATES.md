# Android app updates

Phone and TV release builds check public GitHub releases on startup and when returning to the foreground, at most once every 15 minutes after a successful check; failures retry after one minute. Changing the Release Cycle triggers an immediate check. Admin profiles choose Nightly or Stable in Settings. Stable is the default, independently of the installed release stage:

- **Stable:** published stable releases only.
- **Nightly:** Nightly or Stable releases.

The device-local preference survives restarts and sign-out. A stored legacy RC preference falls back to Stable. Changing the choice rechecks releases unless installation is underway. Only higher-code APKs are offered; drafts and historical alpha/RC tags are ignored. Checks inspect ten pages of thirty releases; offline/failed checks stay quiet.

APKs through `26.10.29-nightly` lack this setting. `26.10.25-Stable` also predates nightly tag/URL recognition and needs a manual signed update preserving data. Published APKs remain immutable.

Higher version codes offer APK installation. A Nightly installation may update to the fresh Stable APK built from that Nightly source; the same image digest is reused. APK acceptance verifies checksum, target and signing identity before installation. Embedded package versions remain authoritative; no same-APK label-only promotion is offered.

GitHub requests use system HTTPS trust without server credentials, cookies or LAN certificate exceptions. Downloads are bounded to 256 MiB in app-private storage. Only project release URLs and GitHub asset hosts are accepted. APK checksum, package, exact version, target marker and installed signing certificate must match. Certificate rotation requires review.

`android-release.json` includes `artifacts.phone` and `artifacts.tv`, each with `file` and `sha256`. APKs declare `io.github.alreadyonapp.alreadyon.UPDATE_TARGET` to prevent crossing targets. Older descriptors without explicit targets remain valid for release promotion but are skipped by the updater.

Update/Cancel works before sign-in with touch or remote focus. Cancellation stops downloads and abandons installation. Android permission and installation confirmation remain user decisions. Pending installation state is saved before commit, which starts only in a resumed app. Callbacks or package replacement attempt relaunch; Android may require Open/manual launch. Installed identity is verified before cleanup. Account/profile/session storage is untouched.

Debug packages skip production checks. Physical phone/TV acceptance remains separate from emulator validation. Test network failures, corruption, wrong targets, permissions, cancellation, process death, replacement and settings continuity.

Run `python3 scripts/android/test-updates.py --serial EMULATOR_SERIAL --sdk SDK_DIRECTORY` on an emulator; add `--java-home JDK_DIRECTORY` when needed. Synthetic phone/TV packages exercise Nightly/Stable selection, dialogs and approved APK replacement, then remove only their own packages. See [Android](ANDROID.md) and [release policy](ANDROID_RELEASES.md).
