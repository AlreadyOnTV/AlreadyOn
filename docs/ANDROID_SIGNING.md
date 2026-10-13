# Android release signing

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

Create or select a maintainer-owned release keystore before the first publication. Android Studio's **Generate Signed Bundle / APK** or JDK `keytool` can generate it locally; no purchased certificate is necessary. Keep the keystore and passwords securely backed up outside Git. Do not use a temporary CI/debug key or generate a new key on every build: future APK updates require the established signing identity.

In repository **Settings → Secrets and variables → Actions**, add:

| Repository secret | Value |
| --- | --- |
| `ANDROID_KEYSTORE_BASE64` | Base64-encoded release keystore file |
| `ANDROID_KEYSTORE_PASSWORD` | Keystore password |
| `ANDROID_KEY_ALIAS` | Signing key alias within the keystore |
| `ANDROID_KEY_PASSWORD` | Key password, which may equal the keystore password |

Base64 is transport encoding, not encryption; treat it as private key material. Use GitHub's secret UI or `gh secret set` with file/stdin input; never paste keys into issues, logs or source. The workflow decodes the keystore into runner temporary storage, passes passwords through process environment references to `apksigner`, verifies the signature, and removes the decoded file. Missing secrets fail publication clearly. The job's GitHub token needs `contents: write`; no personal token is required.

The signed release uses `io.github.alreadyonapp.alreadyon`. The current developer app uses `io.github.alreadyonapp.alreadyon.debug`, so its data and signing identity are separate; installing the release creates a separate app. Back up the permanent key before distributing the first release.

## Google Play

Google Play publication is separate from [GitHub APK releases](ANDROID_RELEASES.md). It requires a developer account, a signed Android App Bundle and Play App Signing enrollment. Play distinguishes the upload key from the app signing key used for installed APKs. For compatible updates between GitHub and Play distribution, arrange matching app signing identity when enrolling. You can supply your own app signing key to Google; using an unrelated Google-generated key prevents ordinary cross-channel updates. Consider a separate upload key for Play.

See [Android signing](https://developer.android.com/studio/publish/app-signing) and [Play App Signing](https://support.google.com/googleplay/android-developer/answer/9842756?hl=en).
