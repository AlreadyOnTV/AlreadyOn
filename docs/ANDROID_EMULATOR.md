# Android emulator testing

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

Install Android Studio or the command-line SDK and a compatible emulator image. Confirm hardware virtualization is available on the host. Store local SDK paths and device identifiers outside Git.

Build the phone client using [its instructions](../apps/android/README.md). Select the intended emulator explicitly when installing or collecting logs, especially if a physical device is also connected.

An emulator's loopback is separate from the host's loopback. Use the emulator's documented host-network route and a certificate valid for the selected server name. Do not turn off TLS globally to work around routing or certificate problems.

Use synthetic media and a disposable server for destructive tests. Validate authentication, guide navigation, tuning, transitions, screen rotation and background recovery. Sanitize logs before sharing them: tokens, cookies, usernames, media names and local addresses may be present.

For Google TV, enable the AVD hardware keyboard and use arrow keys plus Enter as the remote. Back returns from fullscreen to the guide. TV setup forms are centered. Manual address entry is available while discovery runs; typing does not stop discovery, and Rescan restarts it. TV sign-in fields are narrower, profile buttons use half the guide width, and the first profile is focused. The logo is non-interactive, Username receives initial focus, and the guide focuses the current airing. Phone and TV profile menus offer Dashboard, Profile Settings, Switch Profile and Sign Out, in that order, for every signed-in profile. The PIN keypad initially focuses 1; confirm the username to open the centered PIN keypad, then select “✓ Done” to dismiss it and Connect to sign in. Enable a touch screen in the test AVD if mouse clicks are wanted; TV profiles normally disable touch. If the emulator window stops updating or reports color-buffer errors, cold boot with software graphics. These are emulator settings, not TV app requirements.

Emulator success does not establish physical-device codec, HDR, hardware-decoder or television behavior. Record those checks separately.

See [manual connection](SERVER_CONNECTION.md) for the separate hostname/port fields and smaller TV server PIN keypad.
