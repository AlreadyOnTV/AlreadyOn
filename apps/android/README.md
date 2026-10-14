# Android phone client

[![AlreadyOn](../../web/public/branding/header-doc.svg)](../../README.md)

A Kotlin/Compose client with Media3 playback for an AlreadyOn installation. Both mobile and TV search for local servers for 30 seconds, then offer manual HTTPS address entry. The server certificate is accepted automatically; sign in with username and the TV PIN set for your account in WebUI Admin Settings.

## Build

Use JDK 17, the committed Gradle wrapper and Android SDK components declared in the build files. The current project compiles against SDK 37 (target SDK 36) and supports API 26 or later.

From this directory run `./gradlew :app:assembleDebug` for mobile or `./gradlew :tv:assembleDebug` for TV. Install the generated debug APK on a selected emulator or test device. Keep SDK paths in ignored `local.properties`; never commit signing keys, credentials or generated APKs.

The application ID is `io.github.alreadyonapp.alreadyon`. Release signing and distribution belong to the maintainer; see [APK release workflow and signing setup](../../docs/ANDROID_RELEASES.md). The separate [Google TV target](../../docs/GOOGLE_TV.md) shares the phone model/player; automatic signed-update work remains separate.

In fullscreen portrait or landscape, swipe up to the next higher available channel or down to the next lower one. Each deliberate swipe changes one channel; the lineup ends do not wrap. Fullscreen, orientation, mute and quality stay unchanged. Subtitle Off or the selected language carries across channels when a matching track is available.

## Test

Use an isolated server and check login/logout, profile permissions, guide navigation, tuning, program boundaries, track selection, rotation and background recovery. Verify physical-device playback independently of emulator behavior.

No fingerprint confirmation is requested. Subsequent connections validate the accepted instance trust anchor. See [Android architecture](../../docs/ANDROID.md), [emulator setup](../../docs/ANDROID_EMULATOR.md) and [OpenAPI](../../api/openapi.yaml).

The October 18 toolchain pairs Gradle 9.8.1 with AGP 9.4.1 and built-in Kotlin. Run Gradle with JDK 17; both apps keep Java 17 bytecode, compile SDK 37 and target SDK 36. Shared TV Kotlin directories use `android.sourceSets.kotlin`; the external Kotlin Android plugin is unnecessary. Compose retains its explicitly pinned compiler plugin. See [dependency maintenance](../../docs/DEPENDENCY_MAINTENANCE.md) for batch validation.
