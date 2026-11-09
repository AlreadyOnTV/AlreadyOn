# Validation and delivery

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

Maintainers control releases; keep credentials private. Current release titles are `YY.MM.DD-nightly` and `YY.MM.DD-stable`. [Release notes](RELEASE_NOTES.md) define their contents.

## Development and releases

The release date is generated in `America/New_York` when Nightly publication is dispatched.

Work on `dev`, validate with the HTTPS preview, disposable Docker tests and phone/TV builds, then open a PR to `main`. PRs run CodeQL, dependency review, Android and full e2e/runtime checks. The merge job waits for all gates. Dependabot opens grouped weekly PRs; see [maintenance](DEPENDENCY_MAINTENANCE.md).

Maintainers manually dispatch Nightly from the current `dev` or `main` tip. It validates the image, then publishes `nightly` and date aliases plus signed `YY.MM.DD-nightly` phone/TV APKs. A same-date rerun replaces assets only after all gates pass and with higher APK codes; the date tag advances to that SHA.

After Nightly acceptance, dispatch **Build and Publish** with `operation: stable` on `main`. It verifies Nightly provenance, reuses its image digest for `latest` and builds fresh Stable APKs from that commit.

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

On approval, deploy the tested image and verify revision, health, service count, mounts and playback. `scripts/deploy-production.sh <ssh-host> [compose-directory]` transfers it with updates paused, using the host's configured Compose tag. Digest-pinned references require maintainer review; preserve live data.

Preserve the exact tested source and image digest through publication. Main intentionally has no branch protection, so the PR merge job performs a direct merge pinned to the checked head SHA after quality, Android, E2E/runtime, dependency-review and both CodeQL analyses succeed. A failed gate or changed head leaves the PR open. Do not rebuild or replace previously signed APKs or published release tags outside the validated same-date workflow.

Dev changes reach users only after the reviewed main PR merges. See [retention](IMAGE_RETENTION.md).

## Public automation

PR validation scans the exact unpublished candidate with read-only credentials; publication credentials are unavailable. Security Scan covers the final runtime image and Android release-runtime dependencies, not CI/build/test tools. Critical/High findings block publication; secret matches require private review. New cycles require [signed image evidence](ARTIFACT_PROVENANCE.md); [weekly Dependabot PRs](DEPENDENCY_MAINTENANCE.md) use the same validation gates.

CodeQL analyzes Go/TypeScript only on PRs to `dev` or `main`; Pages uses its independent redirect workflow. Nightly and Stable APKs are published by Build and Publish; see [Android policy](ANDROID_RELEASES.md).

New images use `ghcr.io/alreadyontv/alreadyon`. Go module and Android package identities stay compatible. Stable publication is manual and reuses the tested Nightly digest. **Daily Build Base Image** refreshes shared bases and removes stale base versions after 30 days; CI bases pin their runtime digest.
