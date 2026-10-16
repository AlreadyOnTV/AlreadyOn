# TV device review

Use an explicitly selected physical TV. Install `:tv:assembleDeviceReview` to measure the optimized, non-debuggable TV release configuration with the debug package/key; updates preserve its encrypted session. Its launcher label is **AlreadyOn TV Test**, distinct from the public app. Debug builds are useful for instrumentation, but their timing and memory are not release measurements.

The release TV build enables shrinking and optimization. Start the signed-in guide, then run:

```sh
python3 scripts/android/review-tv.py --serial DEVICE_SERIAL --sdk /path/to/Android/Sdk
```

The driver checks guide browsing, fullscreen controls, nested menus, channel switching and Back restoration using remote keys. It never clears data, signs out, edits server settings or seeds a live database. A protected profile may require its profile PIN again after restart; account sign-in must persist.

Separately measure navigation with `adb -s DEVICE_SERIAL shell dumpsys gfxinfo io.github.alreadyonapp.alreadyon.tv.debug reset`, exercise remote keys, then collect `dumpsys gfxinfo` and `dumpsys meminfo`. Record sample count and percentiles; frame time is not end-to-end key latency. Do not compare an instrumented/debug build with a release build as if code changes alone caused the difference.

To collect credential-free playback timings, enable the `AlreadyOnPlayback` DEBUG log tag through ADB and read that tag from logcat. First-frame timing covers tune request through first rendered frame; preparation timing identifies the server/network portion. Buffer-ready events measure player buffering separately. Compare cold and warm channels, monitor transitions and buffering, and report any sample exceeding the two-second target. Faster initial buffering alone cannot guarantee server preparation or network throughput.

Persistent native sessions still respect explicit Sign Out, WebUI device revocation and disabled accounts. Validate revocation with isolated auth fixtures; do not sign out a household's other devices as part of a navigation review. Physical reboot and low-memory-device certification remain separate checks.

Separately verify single Back opens the profile menu, a second Back within two seconds exits to Home, delayed Back restores the guide, menu navigation cancels quick exit, and Exit App preserves sign-in on reopening. Record per-run measurements and screenshots in the review or issue rather than maintaining historical benchmarks here.
