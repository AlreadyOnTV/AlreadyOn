# Product screenshot provenance

Captured on October 13, 2026 from the actual AlreadyOn interfaces for the product website.

- `web-guide.png`: Chromium, 1600 × 1000, current compiled WebUI, actual decoded playback (1280-pixel video, ready state 4).
- `android-phone-guide.png`: native Android debug app, Pixel 10 Pro emulator, 1080 × 2400.
- `google-tv-guide.png`: native Google TV debug app, Google TV 4K emulator at 1920 × 1080.

All captures use an isolated disposable HTTPS server and SQLite database. The eight demo channels, program names, metadata and mountain illustration are original synthetic content. The illustration was drawn in code and encoded as a silent video for real playback. No commercial program artwork, downloaded media, live accounts, credentials, personal library information or workstation paths appear in these images. The product UI has not been generated, recreated or composited. Presentation framing and gloss belong to the website CSS.

The native APKs were built with `:app:assembleDebug :tv:assembleDebug`; both succeeded. Screenshots demonstrate the interfaces, not physical-device release acceptance or the full release validation suite. Emulator app data was cleared only on the explicitly selected disposable capture emulators, and owned emulators and the fixture server were stopped after capture.

## Guide continuity for future captures

The screenshots were recaptured with every visible channel continuously scheduled from the current rounded half-hour minus one hour through the following 26 hours. Seed slots on half-hour boundaries, with each slot's end equal to the next slot's start, and use media long enough to play through the entire slot. Do not shift the fixture schedule to capture time: doing so leaves an empty first timeslot when the guide rounds its window backwards.

Before capture, verify all eight channels have a program whose start is at or before the earliest visible guide boundary and whose end is after it. The disposable SQLite schedule check confirmed 8/8 channels covering that boundary and adjacent intervals with no gaps across the full fixture window. Visually inspect the leftmost program column in each actual WebUI, phone and TV capture. The recaptured WebUI shows programs beginning at 2:00pm on every channel; native captures use the same continuous fixture. Images are direct browser/emulator screenshots, with no editing of program cells or UI.
