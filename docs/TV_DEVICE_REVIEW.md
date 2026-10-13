# TV device review

Use an explicitly selected physical TV. Install `:tv:assembleDeviceReview` to measure the optimized, non-debuggable TV release configuration with the debug package/key; updates preserve its encrypted session. Its launcher label is **AlreadyOn TV Test**, distinct from the public app. Debug builds are useful for instrumentation, but their timing and memory are not release measurements.

The release TV build enables shrinking and optimization. Start the signed-in guide, then run:

```sh
python3 scripts/android/review-tv.py --serial DEVICE_SERIAL --sdk /path/to/Android/Sdk
```

The driver uses only remote keys, verifies guide Up/Down and horizontal browsing, fullscreen entry, overlay focus, current submenu options, pause/resume, guide Back restoration, mini-player/profile focus and exit, then force-stops/restarts the app to verify session restoration. It never clears data, signs out, edits server settings or seeds a live database. A protected profile may require its profile PIN again after restart; account sign-in must persist.

Separately measure navigation with `adb -s DEVICE_SERIAL shell dumpsys gfxinfo io.github.alreadyonapp.alreadyon.tv.debug reset`, exercise remote keys, then collect `dumpsys gfxinfo` and `dumpsys meminfo`. Record sample count and percentiles; frame time is not end-to-end key latency. Do not compare an instrumented/debug build with a release build as if code changes alone caused the difference.

To collect credential-free playback timings, enable the `AlreadyOnPlayback` DEBUG log tag through ADB and read that tag from logcat. First-frame timing covers tune request through first rendered frame; preparation timing identifies the server/network portion. Buffer-ready events measure player buffering separately. Compare cold and warm channels, monitor transitions and buffering, and report any sample exceeding the two-second target. Faster initial buffering alone cannot guarantee server preparation or network throughput.

Persistent native sessions still respect explicit Sign Out, WebUI device revocation and disabled accounts. Validate revocation with isolated auth fixtures; do not sign out a household's other devices as part of a navigation review. Physical reboot and low-memory-device certification remain separate checks.

Onn 4K Pro review, October 10, 2026: the optimized device-review build produced 182 navigation frames, with median 19 ms, p90 34 ms, p95 44 ms and p99 61 ms; 27 frames (14.84%) were classified as janky. App PSS was 53,711 KiB. Sample first-frame tune times were 955, 958, 1,950 and 2,275 ms. The two-second target is therefore not consistently met; the slow sample included 1,915 ms of server preparation. These are measurements on one device, not certification for weaker hardware or zero input lag.
