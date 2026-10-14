# Validation and delivery

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

Maintainers control releases; keep credentials private. [Release notes](RELEASE_NOTES.md) define date/stage-only titles, Download Now and three product sections and predecessor-tag comparisons. Titles preserve artifact tags.

## Development and releases

`release-date.txt` pins the active cycle to `26.10.18`. Reuse until Stable on main, then begin another.

Use the primary checkout, `26.10.18`, and HTTPS preview on port 5173. Dated dev PRs run full quality, source/browser/runtime and security gates. Explicit active-date dispatches run these gates and publish alpha artifacts without merging or deleting the branch. Dev merges republish date/`nightly` images and Android candidates, advance the date tag and delete the merged branch. Hotfixes repeat the gates.

Local checks: `make lint`, documentation and syntax checks.


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

On approval, deploy the exact tested image and verify revision, health, service count, mounts and relevant playback. `scripts/deploy-production.sh <ssh-host> [compose-directory]` transfers it with the updater paused. Preserve live data.

After alpha approval run `python3 scripts/promote.py test YY.MM.DD`; after RC approval run `python3 scripts/promote.py main`. These create fast-forward or exact-tree merge commits only, require image/APK provenance, and reuse artifacts without e2e reruns. Resolve conflicts on dev; preserve branch protection.

Dev builds follow validated PR merges; guarded test/main merges promote existing images. See [retention](IMAGE_RETENTION.md).

## Public automation

PR validation scans the exact patched candidate with read-only credentials; publication credentials are unavailable. Critical/High findings block publication; secret matches require private review. Scanner failures block the gate. Runtime/CI images are scanned. New cycles require [signed image evidence](ARTIFACT_PROVENANCE.md); [weekly dependency review](DEPENDENCY_MAINTENANCE.md) never publishes.

CodeQL analyzes Go/TypeScript on dev/test/main; Pages uses its independent organization-site workflow. Android automation builds alpha on dev, RC on test and Stable from RC artifacts on main; see [Android policy](ANDROID_RELEASES.md).

New images use `ghcr.io/alreadyontv/alreadyon`. Go module and Android package identities stay compatible. Stable is intended for October 18, 2026, after alpha/RC acceptance; no automatic deployment is scheduled. **Refresh Base Images** rebuilds shared bases; CI bases pin their runtime digest. Promotions reuse digests.
