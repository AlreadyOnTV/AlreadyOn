# Android emulator testing

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

Install Android Studio or the command-line SDK and a compatible emulator image. Confirm hardware virtualization is available on the host. Store local SDK paths and device identifiers outside Git.

Build the phone client using [its instructions](../apps/android/README.md). Select the intended emulator explicitly when installing or collecting logs, especially if a physical device is also connected.

An emulator's loopback is separate from the host's loopback. Use the emulator's documented host-network route and a certificate valid for the selected server name. Do not turn off TLS globally to work around routing or certificate problems.

Use synthetic media and a disposable server for destructive tests. Validate authentication, guide navigation, tuning, transitions, screen rotation and background recovery. Sanitize logs before sharing them: tokens, cookies, usernames, media names and local addresses may be present.

For Google TV, enable the AVD hardware keyboard and use arrows, Enter/Select and Back as a remote. Use [TV setup and fixtures](GOOGLE_TV.md), [remote navigation](TV_REMOTE_NAVIGATION.md) and [manual connection](SERVER_CONNECTION.md) for current behavior. Enable a touch screen only if mouse clicks are wanted; TV profiles normally disable touch. If the emulator window stops updating or reports color-buffer errors, cold boot with software graphics. These are emulator settings, not TV app requirements.

For focused TV onboarding coverage, run `scripts/android/test-tv.py --serial <emulator-serial> --sdk <sdk-path> --pairing-only`. This uses a disposable server and synthetic account, checks both discovery and manual entry through profile selection, and stops before playback checks. Omit `--pairing-only` for the broader navigation/playback test. `ServerPinFieldTest` separately exercises the installed numeric keyboard; its TV remote sequence targets the Google TV emulator’s numeric grid.

Emulator success does not establish physical-device codec, HDR, hardware-decoder or television behavior. Record those checks separately.

Both targets explicitly use Espresso 3.7.0 alongside AndroidX Test runner/rules 1.7.0. Compose's older transitive Espresso uses removed input-manager APIs on API 37. Keep the test-runtime dependency aligned in both targets; do not bypass Espresso synchronization or hidden-API enforcement. Run `GuideRestorationTest` on API 37 phone and API 36 TV emulators after test-framework changes.

From `apps/android`, select the emulator and test class explicitly:

```sh
ANDROID_SERIAL=<serial> ./gradlew :app:connectedDebugAndroidTest \
  -Pandroid.testInstrumentationRunnerArguments.class=io.github.alreadyonapp.alreadyon.GuideRestorationTest
```

Use `:tv:connectedDebugAndroidTest` for TV. Run additional classes separately; verify test counts and failures in Gradle's XML reports.

See [manual connection](SERVER_CONNECTION.md) for the separate hostname/port fields and device numeric keyboard for server PIN entry.
