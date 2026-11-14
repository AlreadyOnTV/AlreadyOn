# Packaged-runtime tests

[![AlreadyOn](../../web/public/branding/header-doc.svg)](../../README.md)

Run `make test-container` to validate the production image in a disposable container with synthetic fixtures and temporary storage. The harness owns its ports, database and cleanup; it does not reuse a live installation.

Checks exercise runtime packaging, startup, TLS and media/API behavior. Settings and channel creation acknowledge saved preferences before processing finishes; the harness waits for the durable applied revision, then reloads the channel before asserting its lineup or tuning playback. Inspect failures before changing assertions. A native Go or browser pass does not prove the packaged image contains the required tools, permissions or codec support.

Use a supported container engine and keep generated fixtures outside Git. Record the tested image and actual results. Live deployment verification remains a separate non-destructive step.

See [delivery](../../docs/CI_CD.md), [fixtures](../fixtures/README.md) and [deployment](../../deploy/README.md).
