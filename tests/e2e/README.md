# Browser end-to-end tests

[![AlreadyOn](../../web/public/branding/header-doc.svg)](../../README.md)

`make test-e2e` runs playback, channel filters, library catalog, profile autosave and settings feedback browser suites against an isolated synthetic server. The harness prepares generated fixtures and temporary application storage; it must not target a live preview or production installation.

Player menu checks require a detected audio language when known and omit the suffix for untagged audio.

The suite covers authentication, guide navigation, tuning and playback behavior across synthetic media scenarios. Fixtures and clocks should make failures reproducible. Preserve useful assertions when fixing timing problems; waiting for a real readiness signal is preferable to hiding a failure with arbitrary sleeps.

Run `PLAYBACK_TEST_ONLY=admin-metrics-logs make test-e2e` for isolated dashboard and log checks. Disk fixtures use per-mount bandwidth, IOPS and latency, including unavailable readings; assertions cover the four throughput lines, missing-sample gaps, capacity table and expanded-chart layout.

Subtitle defaults select the language and mode by their visible labels. The profile scenario saves German with Always on, then checks playback captions and the C shortcut; selector order must not determine the setting being changed.

Full release runs require maintainer approval. Record the exact command, exit status and failing scenario. Store screenshots, logs and transcripts in ignored local locations and sanitize them before sharing.

A browser pass does not certify every device, codec or GPU. Use [packaged-runtime tests](../integration/README.md) for image behavior and separate physical-device validation where necessary. See [delivery](../../docs/CI_CD.md).

For focused WebUI profile autosave checks, start the development preview and run `node tests/e2e/profile-autosave.cjs https://localhost:5173`. Every API request is intercepted with an in-memory synthetic profile; no preview profile or PIN is changed. Coverage includes unchanged-value suppression, PIN confirmation, five-second success feedback and fade, queued edits, failure/retry, and compact layout. This is browser behavior coverage; the existing isolated settings scenario covers persistence against the server.

For focused guide restoration checks, run `node tests/e2e/guide-live.cjs https://localhost:5173`, or select `guide-live` in the isolated harness. All API calls are intercepted; preview data is untouched. The accelerated clock covers fullscreen CH 101 to CH 115, an hour across midnight, program/half-hour boundaries, lower-numbered channels, the final channel at the top, and a failed guide refresh with cached airings. These checks exercise guide state and geometry, not media decoding or physical-device playback.
