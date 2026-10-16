# Browser end-to-end tests

[![AlreadyOn](../../web/public/branding/header-doc.svg)](../../README.md)

`make test-e2e` runs the full browser suite against an isolated synthetic server. The harness prepares generated fixtures and temporary application storage; it must not target a live preview or production installation.

Player menu checks require a detected audio language when known and omit the suffix for untagged audio.

The suite covers authentication, guide navigation, tuning and playback behavior across synthetic media scenarios. Fixtures and clocks should make failures reproducible. Preserve useful assertions when fixing timing problems; waiting for a real readiness signal is preferable to hiding a failure with arbitrary sleeps.

Subtitle defaults select the language and mode by their visible labels. The profile scenario saves German with Always on, then checks playback captions and the C shortcut; selector order must not determine the setting being changed.

Full release runs require maintainer approval. Record the exact command, exit status and failing scenario. Store screenshots, logs and transcripts in ignored local locations and sanitize them before sharing.

A browser pass does not certify every device, codec or GPU. Use [packaged-runtime tests](../integration/README.md) for image behavior and separate physical-device validation where necessary. See [delivery](../../docs/CI_CD.md).
