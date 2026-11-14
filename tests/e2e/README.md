# Browser end-to-end tests

[![AlreadyOn](../../web/public/branding/header-doc.svg)](../../README.md)

`make test-e2e` runs playback, channel filters, library catalog, profile autosave, library path customization and settings feedback browser suites against an isolated synthetic server. The harness uses temporary fixtures/storage; never target a live installation.

Folder checks assert `path (Found N)` and immediate persistence independently of commercial break drafts.

Player menu checks require a detected audio language when known and omit the suffix for untagged audio.

The suite covers authentication, guide navigation, tuning and synthetic playback. Use reproducible fixtures/clocks and readiness signals instead of arbitrary sleeps.

Run `PLAYBACK_TEST_ONLY=admin-metrics-logs make test-e2e` for isolated dashboard and log checks. Disk fixtures use per-mount bandwidth, IOPS and latency, including unavailable readings; assertions cover the four throughput lines, missing-sample gaps, capacity table and expanded-chart layout.
Activity checks cover concurrent sessions, client-version labels and unknown fallback, hidden latency, and paired source/output formats.

Subtitle defaults select the language and mode by their visible labels. The profile scenario saves German with Always on, then checks playback captions and the C shortcut; selector order must not determine the setting being changed.

Full release runs require maintainer approval. Record the exact command, exit status and failing scenario. Store screenshots, logs and transcripts in ignored local locations and sanitize them before sharing.

A browser pass does not certify every device, codec or GPU. Use [packaged-runtime tests](../integration/README.md) for image behavior and separate physical-device validation where necessary. See [delivery](../../docs/CI_CD.md).

Run `node tests/e2e/playback-capabilities.cjs https://localhost:5173 webkit` (or `chromium`) against the preview for isolated capability checks. All management requests are intercepted. Cases cover managed, standard and prefixed media-source APIs, native HLS without MSE, and unsupported playback. These API-availability scenarios do not emulate iOS codecs or certify iPhone playback. WebKit on Fedora can run in the matching Playwright Ubuntu container with host networking and the checkout mounted read-only.

For focused WebUI profile autosave checks, start the development preview and run `node tests/e2e/profile-autosave.cjs https://localhost:5173`. Every API request is intercepted with an in-memory synthetic profile; no preview profile or PIN is changed. Coverage includes unchanged-value suppression, PIN confirmation, five-second success feedback and fade, queued edits, failure/retry, and compact layout. This is browser behavior coverage; the existing isolated settings scenario covers persistence against the server.

For focused guide checks, run `node tests/e2e/guide-live.cjs https://localhost:5173`, or select `guide-live` in the isolated harness. All API calls are intercepted; preview data is untouched. Controlled time covers keyboard/wheel/touch navigation pauses, deadline extension, programmatic-scroll exclusion, exact expiry and resumed minute polling. Restoration checks cover fullscreen CH 101 to CH 115, midnight/program/half-hour boundaries, lower/final channels and failed refreshes with cached airings. These exercise guide state/geometry, not media decoding or physical-device playback.

Run `PLAYBACK_TEST_ONLY=library-paths make test-e2e` for Customize defaults, Cancel, atomic bulk saves, immediate acknowledgement, background progress/retry, group overrides, empty commercial selections, reload and Remove. GitHub Actions runs real persistence and intercepted failure/layout cases through `test-source` → `make test-e2e`. Intercepted cases also run with `node tests/e2e/library-path-customize-ui.cjs https://localhost:5173` without changing preview data. Worker regressions cover coalescing, cancellation, stale-commit rejection and recovery using isolated databases. `make test` includes WebUI unit tests; lint rejects warnings.
