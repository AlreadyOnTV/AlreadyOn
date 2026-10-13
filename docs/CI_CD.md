# Validation and delivery

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

Maintainers control releases. Checkout ownership and deployment credentials belong in ignored `.private/AGENTS.md`.

## Development and releases

`release-date.txt` pins the active cycle to `2026-10-11`, superseding older previews. Publishing reuses it until Stable on main; afterward a new date can begin.

Use one checkout, local `build`, and preview at `https://localhost:5173`. A dated dev PR runs quality, CodeQL, Go/browser e2e, disposable runtime checks and source/image scanning. After merge, release validation republishes the date image/`nightly`, advances the date tag, builds Android alpha artifacts, and deletes the merged date branch. Hotfixes reuse that date and repeat the PR gates.

Run `make lint`, `python3 scripts/ci/check-documentation.py` and `python3 scripts/ci/check-syntax.py` locally. Lint includes public-guide length and link checks.


## Approved release validation

`make release-check` requires a clean checkout and performs:

1. Static, documentation, syntax and CI policy checks.
2. Production-image build (`make image`).
3. Go regressions using patched production FFmpeg (`make test-go-runtime`).
4. Full synthetic browser suite (`make test-e2e`).
5. Disposable packaged-runtime checks (`make test-container`).

Go tests use temporary storage.

Resolve failures before promotion. Record the commit, image ID/digest and actual results. Live checks are non-destructive; synthetic suites use isolated data.

Before image builds, `scripts/local-bases.py` checks base recipes, parent and age; stale/missing bases rebuild. `make refresh-local-bases` forces refresh.

## Delivery

On approval, deploy the exact tested image and verify revision, health, service count, mounts and relevant playback. `scripts/deploy-production.sh <ssh-host> [compose-directory]` transfers it with the updater paused. Preserve live data.

After alpha approval run `python3 scripts/promote.py test YYYY-MM-DD`; after RC approval run `python3 scripts/promote.py main`. These create fast-forward or exact-tree merge commits only, require image/APK provenance, and reuse artifacts without e2e reruns. Resolve conflicts on dev; preserve branch protection.

Dev builds follow validated PR merges; guarded test/main merges promote existing images. See [retention](IMAGE_RETENTION.md) and [deployment](../deploy/README.md).

## Public automation

PR validation builds patched runtime/CI images and scans the exact candidate with read-only credentials; publication credentials remain unavailable. Critical/High findings block publication; secret matches require private review. Scanner failures block the gate. Runtime and CI images are scanned; see [policy](VULNERABILITY_SCANNING.md).

CodeQL analyzes Go/TypeScript on dev/test/main; Pages publishes from main. Android automation builds alpha on dev, RC on test and Stable from RC artifacts on main; see [Android policy](ANDROID_RELEASES.md).

New images use `ghcr.io/alreadyonapp/alreadyon`. Go module and Android package identities stay compatible. **Refresh Base Images** rebuilds shared bases on the selected branch; CI bases pin their runtime digest. Hosted application builds resolve both digests without rebuilding during promotion.
