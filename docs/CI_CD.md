# Validation and delivery

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

Maintainers control releases; keep credentials private. Current release titles are `YY.MM.DD-nightly` and `YY.MM.DD-stable`. [Release notes](RELEASE_NOTES.md) define their contents.

## Development and releases

The release date is generated in `America/New_York` when a Nightly is built from `main`.

Work on `dev`, validate locally with the HTTPS preview, disposable Docker tests and USB-connected phone/TV builds, then open a PR to `main`. PRs require CodeQL, Dependabot dependency review and full e2e/runtime validation. Enable auto-merge after all required checks pass. Weekly dependency updates integrate only their tested batch into `dev`; see [maintenance](DEPENDENCY_MAINTENANCE.md).

Validated merges to `main` publish the image as `nightly` plus current-date aliases and build signed `YY.MM.DD-nightly` phone/TV APKs. A same-date rerun may replace that date's release assets only after all gates pass and with higher APK version codes; immutable source tags are not moved.

After Nightly acceptance, dispatch **Publish Stable from latest Nightly** on `main`. It verifies the Nightly's source and signed image digest, retags that image as `latest` without rebuilding it, and builds fresh Stable APKs from the same commit.

## Approved release validation

`make release-check` requires a clean checkout and performs:

1. Static, documentation, syntax and CI policy checks.
2. Production-image build (`make image`).
3. Go regressions using patched production FFmpeg (`make test-go-runtime`).
4. Full synthetic browser suite (`make test-e2e`).
5. Disposable packaged-runtime checks (`make test-container`).

Resolve failures. Record the commit, image ID/digest and actual results. Live checks are non-destructive; synthetic suites use isolated data.

Before image builds, `scripts/local-bases.py` checks base recipes, parent and age; stale/missing bases rebuild. `make refresh-local-bases` forces refresh.

## Delivery

On approval, deploy the exact tested image and verify revision, health, service count, mounts and relevant playback. `scripts/deploy-production.sh <ssh-host> [compose-directory]` transfers it with the updater paused, using the host's configured Compose image tag without changing its configuration. Digest-pinned references require maintainer review. Preserve live data.

`main` is the only release branch. Preserve the exact tested source and image digest through publication. Do not rebuild or replace previously signed APKs or published release tags outside the validated same-date workflow.

Dev changes reach users only after the reviewed main PR merges. See [retention](IMAGE_RETENTION.md).

## Public automation

PR validation scans the exact patched candidate with read-only credentials; publication credentials are unavailable. Critical/High findings block publication; secret matches require private review. Scanner failures block the gate. Runtime/CI images are scanned. New cycles require [signed image evidence](ARTIFACT_PROVENANCE.md); [weekly dependency updates](DEPENDENCY_MAINTENANCE.md) publish only validated nightlies.

CodeQL analyzes Go/TypeScript on dev/main; Pages uses its independent organization-site workflow. Android automation builds Nightly APKs from main merges and fresh Stable APKs from the latest validated Nightly; see [Android policy](ANDROID_RELEASES.md).

New images use `ghcr.io/alreadyontv/alreadyon`. Go module and Android package identities stay compatible. Stable publication is manual and reuses the tested Nightly digest. **Refresh Base Images** rebuilds shared bases; CI bases pin their runtime digest.
