# Android

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

The Android clients use Kotlin, Compose and Media3. Phone/tablet and TV discover local HTTPS servers for 30 seconds while offering [manual entry](SERVER_CONNECTION.md). Choosing a server stops discovery. They accept the instance certificate, sign in with username and the WebUI-configured 6–12 digit TV PIN, and share profiles, guide browsing and playback.

Use [the app guide](../apps/android/README.md) to build. Keep `io.github.alreadyonapp.alreadyon`; signing keys and update credentials remain private.

## Boundaries

Management calls use authenticated sessions; media delivery uses scoped credentials. Profile and account permissions remain server-enforced. Persist only necessary session state and review storage, backup and logout behavior before release.

Native onboarding accepts the instance certificate on first connection without a fingerprint confirmation screen and validates subsequent HTTPS requests against that instance certificate. PIN sign-in requires a direct LAN connection. Phone/tablet and TV use the installed numeric keyboard for the visible **TV Pairing Pin**, without an eye control or permanent PIN instruction. Entry remains limited to 12 ASCII digits. Username Next moves to the PIN; Done dismisses the keyboard and focuses Connect when valid. **Connect**, **Go Back** and **Help** share one row. Go Back retains the address draft; Help explains WebUI Settings and the separate profile PIN. Phone **Check Server / Rescan** actions also share a row, with matching extra spacing above both sign-on button rows.

## Validation

Check sign-in, profiles/PINs, channel changes, transitions, tracks, rotation and recovery. Explicit 1080p uses the decoder-supported H.264 High profile on phone and TV; 4K is TV-only and requires both a verified TV decoder and server hardware encoder. Auto transcodes start at 360p SDR. Test picture-in-picture only on phones; TV and WebUI omit it. Use synthetic fixtures; record codec limitations without personal details.

On phones, tap the picture-in-picture video to reveal Android’s menu. Use Play or Pause and Mute or Unmute in the bottom row. Resuming playback rejoins the current live airing. Controls stay visible while tuning. Tap Play to retry after errors.

See [emulator setup](ANDROID_EMULATOR.md), [playback](PLAYBACK.md) and [OpenAPI](../api/openapi.yaml).

See [Google TV](GOOGLE_TV.md) and [TV review](TV_DEVICE_REVIEW.md).

## App updates

Signed releases offer verified APK updates while preserving settings; debug packages skip update checks. Android installation still requires user approval. See [update behavior and synthetic tests](ANDROID_UPDATES.md) for download, validation, replacement and recovery contracts.

See [subtitle modes](SUBTITLES.md).

Phone/TV Sign Out retains the encrypted session. Selecting the recent server revalidates it before skipping credentials; profile PINs still apply. WebUI revocation, expiry and account restrictions require fresh authentication. Network/TLS and scoped playback failures retain the cookie. Only a management authentication challenge expires it; playback capacity errors remain recoverable and identify the server limitation.

Active clients refresh shared profile settings and avatars every five minutes and on app resume, preserving local settings drafts and playback choices.

Phone and TV guide headers show the current device-local time in the profile’s time format. The clock updates on minute boundaries and immediately on resume. Its left inset matches the channel numbers below.

Fullscreen guides stay current. Returning restores the watched row at top, current slot leftmost and live airing in red.
