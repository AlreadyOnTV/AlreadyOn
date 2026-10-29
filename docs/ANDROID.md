# Android

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

The Android phone client uses Kotlin, Compose and Media3. Phone/tablet and TV apps discover local HTTPS servers for 30 seconds while offering [manual entry](SERVER_CONNECTION.md) immediately. Typing does not stop discovery; choosing a server does. After accepting the server certificate automatically, they sign in with username and the account’s 6–12 digit TV PIN configured in WebUI Admin Settings. They share account profiles, guide browsing and playback.

Use [the app guide](../apps/android/README.md) to build. Keep `io.github.alreadyonapp.alreadyon` as the application identity. Signing keys and update credentials belong to the maintainer and must remain private.

## Boundaries

Management calls use authenticated sessions; media delivery uses scoped credentials. Profile and account permissions remain server-enforced. Persist only necessary session state and review storage, backup and logout behavior before release.

Native onboarding accepts the instance certificate on first connection without a fingerprint confirmation screen and validates subsequent HTTPS requests against that instance certificate. PIN sign-in requires a direct LAN connection. Phone/tablet and TV use the installed numeric keyboard for the visible **TV Pairing Pin**, without an eye control or permanent PIN instruction. Entry remains limited to 12 ASCII digits. Username Next moves to the PIN; Done dismisses the keyboard and focuses Connect when valid. **Connect**, **Go Back** and **Help** share one row. Go Back retains the address draft; Help explains WebUI Settings and the separate profile PIN. Phone **Check Server / Rescan** actions also share a row, with matching extra spacing above both sign-on button rows.

## Validation

Check sign-in, profile selection/PIN behavior, channel changes, transitions, tracks, rotation, picture-in-picture and background recovery. Use synthetic fixtures; actual device validation is distinct from emulator or browser testing. Record device/codec limitations without publishing personal host or device details.

See [emulator setup](ANDROID_EMULATOR.md), [playback](PLAYBACK.md) and [OpenAPI](../api/openapi.yaml).

See [Google TV](GOOGLE_TV.md) and [physical TV review](TV_DEVICE_REVIEW.md).

## App updates

Signed releases offer verified APK updates while preserving settings; debug packages skip update checks. Android installation still requires user approval. See [update behavior and synthetic tests](ANDROID_UPDATES.md) for download, validation, replacement and recovery contracts.

See [subtitle modes](SUBTITLES.md).

Phone/TV Sign Out retains the encrypted session. Selecting the recent server revalidates it before skipping credentials; profile PINs still apply. WebUI revocation, expiry and account restrictions require fresh authentication. Network/TLS failures retain the cookie.

Active clients refresh shared profile settings and avatars every five minutes and on app resume, preserving local settings drafts and playback choices.

Phone and TV guide headers show the current device-local time in the profile’s time format. The clock updates on minute boundaries and immediately on resume. Its left inset matches the channel numbers below.

Fullscreen guides stay current. Returning restores the watched row at top, current slot leftmost and live airing in red.
