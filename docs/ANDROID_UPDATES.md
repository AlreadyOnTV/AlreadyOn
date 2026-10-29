# Android app updates

Phone and TV release builds check public GitHub releases on startup. Admin profiles choose the installation's Release Cycle in the top-right profile menu under Settings, above Sign Out. Stable is the default, independently of the installed release stage:

- **Stable:** published stable releases only.
- **Release Candidate:** RC and stable releases.
- **Nightly:** all supported stages, including nightlies and historical alpha/beta releases.

The device-local preference survives restarts and sign-out. Changing it rechecks releases unless installation is underway. It never permits downgrades; returning to Stable may require waiting for a higher-code stable release. Drafts/non-Android tags are ignored. Checks inspect ten pages of thirty releases; offline/failed checks stay quiet.

APKs through `26.10.29-nightly` lack this setting. `26.10.25-Stable` also predates nightly tag/URL recognition and needs a manual signed update preserving data. Published APKs remain immutable.

Higher version codes offer APK installation. Identical-APK promotions to later RC/Stable stages also offer Update/Cancel. Acceptance verifies the installed APK checksum against published target metadata and saves the release label without reinstalling. The prompt and Dashboard App Version show that label across restarts and installer cleanup, only for the matching APK. Embedded package versions remain immutable. Numeric stage comparisons reject older labels and lower codes. Existing clients need a newer updater-enabled APK for promotion prompts.

GitHub requests use system HTTPS trust without server credentials, cookies or LAN certificate exceptions. Downloads are bounded to 256 MiB in app-private storage. Only project release URLs and GitHub asset hosts are accepted. APK checksum, package, exact version, target marker and installed signing certificate must match. Certificate rotation requires review.

`android-release.json` includes `artifacts.phone` and `artifacts.tv`, each with `file` and `sha256`. APKs declare `io.github.alreadyonapp.alreadyon.UPDATE_TARGET` to prevent crossing targets. Older descriptors without explicit targets remain valid for release promotion but are skipped by the updater.

Update/Cancel works before sign-in with touch or remote focus. Cancellation stops downloads and abandons installation. Android permission and installation confirmation remain user decisions. Pending installation state is saved before commit, which starts only in a resumed app. Callbacks or package replacement attempt relaunch; Android may require Open/manual launch. Installed identity is verified before cleanup. Account/profile/session storage is untouched.

Debug packages skip production checks. Physical phone/TV acceptance remains separate from emulator validation. Test network failures, corruption, wrong targets, permissions, cancellation, process death, replacement and settings continuity.

Run `python3 scripts/android/test-updates.py --serial EMULATOR_SERIAL --sdk SDK_DIRECTORY` on an emulator; add `--java-home JDK_DIRECTORY` when needed. Synthetic phone/TV packages exercise selection, promotion labels, dialogs and approved APK replacement, then remove only their own packages. See [Android](ANDROID.md) and [release policy](ANDROID_RELEASES.md).
