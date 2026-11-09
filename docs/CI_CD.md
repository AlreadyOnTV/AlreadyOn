# Validation and delivery

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

Maintainers control releases; keep credentials private. Current release titles are `YY.MM.DD-nightly` and `YY.MM.DD-stable`. [Release notes](RELEASE_NOTES.md) define their contents.

## Development and releases

The release date is generated in `America/New_York` when Nightly publication is dispatched.

Work on `dev`, validate locally with the HTTPS preview, disposable Docker tests and USB-connected phone/TV builds, then open a PR to `main`. PRs to `dev` or `main` run CodeQL, dependency review, Android checks and full e2e/runtime validation. Auto-merge waits for all PR checks. Dependabot opens grouped weekly PRs to `dev`; see [maintenance](DEPENDENCY_MAINTENANCE.md).

Maintainers manually dispatch Nightly publication from the exact current `dev` or `main` tip. The workflow builds and validates the image, then publishes `nightly` plus current-date aliases and signed `YY.MM.DD-nightly` phone/TV APKs. A same-date rerun may replace that date's release assets only after all gates pass and with higher APK version codes; the source date tag advances to the selected SHA.

After Nightly acceptance, dispatch **Build and Publish** with `operation: stable` on `main`. It verifies the Nightly's source and signed image digest, retags that image as `latest` without rebuilding it, and builds fresh Stable APKs from the same commit.

## Approved release validation

`make release-check` requires a clean checkout and performs:

1. Static, documentation, syntax and CI policy checks.
2. Production-image build (`make image`).
3. Go regressions using patched production FFmpeg (`make test-go-runtime`).
4. Full synthetic browser suite (`make test-e2e`).
5. Disposable packaged-runtime checks (`make test-container`).

Resolve failures. Record the commit, image ID/digest and actual results. Live checks are non-destructive; synthetic suites use isolated data.

Daily Base Image refreshes shared bases; `make refresh-local-bases` forces a local refresh.

## Delivery

On approval, deploy the exact tested image and verify revision, health, service count, mounts and relevant playback. `scripts/deploy-production.sh <ssh-host> [compose-directory]` transfers it with the updater paused, using the host's configured Compose image tag without changing its configuration. Digest-pinned references require maintainer review. Preserve live data.

Preserve the exact tested source and image digest through publication. Main intentionally has no branch protection; the PR auto-merge workflow waits for quality, Android, E2E/runtime, CodeQL and dependency-review checks. Do not rebuild or replace previously signed APKs or published release tags outside the validated same-date workflow.

Dev changes reach users only after the reviewed main PR merges. See [retention](IMAGE_RETENTION.md).

## Public automation

PR validation scans the exact unpublished candidate with read-only credentials; publication credentials are unavailable. Security Scan covers the final runtime image and Android release-runtime dependencies, not CI/build/test tools. Critical/High findings block publication; secret matches require private review. New cycles require [signed image evidence](ARTIFACT_PROVENANCE.md); [weekly Dependabot PRs](DEPENDENCY_MAINTENANCE.md) use the same validation gates.

CodeQL analyzes Go/TypeScript only on PRs to `dev` or `main`; Pages uses its independent redirect workflow. Nightly and Stable APKs are published by Build and Publish; see [Android policy](ANDROID_RELEASES.md).

New images use `ghcr.io/alreadyontv/alreadyon`. Go module and Android package identities stay compatible. Stable publication is manual and reuses the tested Nightly digest. **Daily Build Base Image** refreshes shared bases and removes stale base versions after 30 days; CI bases pin their runtime digest.
