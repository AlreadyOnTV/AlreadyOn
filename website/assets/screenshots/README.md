# Product screenshot provenance

Captured on October 13, 2026 from the actual AlreadyOn interfaces for the product website.

- `web-guide.png`: Chromium, 1600 × 1000, current compiled WebUI, actual decoded playback (1280-pixel video, ready state 4).
- `android-phone-guide.png`: native Android debug app, Pixel 10 Pro emulator, 1080 × 2400.
- `google-tv-guide.png`: native Google TV debug app, Google TV 4K emulator at 1920 × 1080.

All captures use an isolated disposable HTTPS server and SQLite database. The eight demo channels, program names, metadata and mountain illustration are original synthetic content. The illustration was drawn in code and encoded as a silent video for real playback. No commercial program artwork, downloaded media, live accounts, credentials, personal library information or workstation paths appear in these images. The product UI has not been generated, recreated or composited. Presentation framing and gloss belong to the website CSS.

The native APKs were built with `:app:assembleDebug :tv:assembleDebug`; both succeeded. Screenshots demonstrate the interfaces, not physical-device release acceptance or the full release validation suite. Emulator app data was cleared only on the explicitly selected disposable capture emulators, and owned emulators and the fixture server were stopped after capture.
