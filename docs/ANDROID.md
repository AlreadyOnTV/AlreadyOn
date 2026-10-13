# Android

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

The Android phone client uses Kotlin, Compose and Media3. Phone/tablet and TV apps discover local HTTPS servers for 30 seconds while offering manual address entry immediately. Typing does not stop discovery; choosing a server does. After accepting the server certificate automatically, they sign in with username and the account’s 6–12 digit TV PIN configured in WebUI Admin Settings. They share account profiles, guide browsing and playback.

Use [the app guide](../apps/android/README.md) to build. Keep `io.github.alreadyonapp.alreadyon` as the application identity. Signing keys and update credentials belong to the maintainer and must remain private.

## Boundaries

Management calls use authenticated sessions; media delivery uses scoped credentials. Profile and account permissions remain server-enforced. Persist only necessary session state and review storage, backup and logout behavior before release.

Native onboarding accepts the instance certificate on first connection without a fingerprint confirmation screen and validates subsequent HTTPS requests against that instance certificate. PIN sign-in requires a direct LAN connection.

## Validation

Check sign-in, profile selection/PIN behavior, channel changes, transitions, tracks, rotation, picture-in-picture and background recovery. Use synthetic fixtures; actual device validation is distinct from emulator or browser testing. Record device/codec limitations without publishing personal host or device details. To test mobile onboarding without changing an existing installation’s sessions, build debug with `-PandroidDebugSuffix=.pairingtest.debug`; release identity stays unchanged.

See [emulator setup](ANDROID_EMULATOR.md), [playback](PLAYBACK.md) and [API compatibility](../api/COMPATIBILITY.md).

The [Google TV target](GOOGLE_TV.md) shares this infrastructure, with TV presentation, remote navigation and local account PIN pairing.

See [physical TV review](TV_DEVICE_REVIEW.md) for safe remote-driven checks and release performance measurement.

## App updates

Signed phone and TV apps check GitHub on startup without delaying viewing. **Update Available** shows installed/available versions and Update/Cancel. APKs with a published stable counterpart receive stable updates; previews also accept alpha/RC releases. Equal APK version codes are not updates, even after promotion.

Updates use the matching device APK and verify checksum, package, target and signing certificate before asking Android to install. Allow installs from AlreadyOn in Android settings when prompted. Approve the system installer; downloads show progress and can be cancelled. Existing login/settings remain intact. After installation the app attempts to reopen and removes its downloaded APK; if necessary select Open or launch it manually. Interrupted installs offer Continue Installation or Cancel on reopening.

Install the first updater-enabled APK manually. Debug packages skip production update checks. See [updater behavior and tests](ANDROID_UPDATES.md). Missing metadata, network failures and older package identities never trigger an unsafe replacement.

See [subtitle modes](SUBTITLES.md).
