# Android APK releases

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

## Release lifecycle

Use the active dated branch and [development setup](DEVELOPMENT.md). Test at <https://localhost:5173>; open its dev PR after approval.

| Promotion | Artifact tag | Release title | Publication |
| --- | --- | --- | --- |
| Merged date PR into `dev` | `YY.MM.DD-alphaN` | `YY.MM.DD-alphaN` | Fresh signed APKs and image tagged `nightly` plus `YY.MM.DD` |
| Merge tested date commit from `dev` to `test` | `YY.MM.DD-RCN` | `YY.MM.DD-rcN` | Retag image `latest`; publish/copy tested APK bytes |
| Merge current `test` release into `main` | `YY.MM.DD-Stable` | `YY.MM.DD-stable` | Retag image `stable`; publish the latest RC APK bytes |

RC1 normally reuses alpha APKs. For an approved direct RC1 build, reserve an unpublished `YY.MM.DD-RC1` draft targeting the exact dev source before allocation. Replacing RC1 requires explicit approval, removal of its release/tag, and a replacement draft with a new version code. Dev builds the draft after validation; test publishes its exact assets. Later hotfixes draft the next RC. Stable copies the latest RC and locks the date.

For explicitly approved direct-to-Stable delivery, dispatch **Build Date Release Image** on `dev` with `publication=stable`. It runs the same full validation, vulnerability and signed-image gates, then builds/signs the new phone/TV APKs once with the Stable version and publishes only `YY.MM.DD-Stable`. No alpha or RC release/tag is allocated. Reconcile historical promotion merges on the dated branch before its dev PR so `test` and `main` can fast-forward to the identical validated dev commit. Their Android workflows verify the existing exact-source Stable assets without creating an RC or rebuilding APKs. Cancel an automatically started candidate build before publication when choosing this explicit delivery path. Direct Stable retains the dated branch until the maintainer retires it after delivery verification.

The date tag advances after dev validation. Changelogs compare predecessor tags, including server/WebUI changes. Identical-artifact promotions retain known issues without new change claims. Promotions never rebuild/re-sign.

Each new release attaches both APKs, `SHA256SUMS`, `android-release.json`, three inventory-only CycloneDX SBOMs and `sbom-manifest.json`. Inventories bind the runtime digest and signed APK hashes to the source; Android provenance binds their manifest. Promotions preserve these bytes. Older releases without inventories remain compatible. Promotions retain the APK `versionName`/`versionCode`; titles do not rename tags.

Approved active-date dispatches validate/publish alpha without merging or deleting the branch; see [identity/cycle](ORGANIZATION.md).

## Automation and gates

Review [Stable readiness](STABLE_READINESS.md) before main promotion.

Date PRs run quality, CodeQL and full Go/browser e2e. Dev merge validation publishes the date image and alpha APKs, advances the date tag, then removes the merged date branch. `python3 scripts/promote.py test YY.MM.DD` verifies image/alpha APK provenance before merging to test; `python3 scripts/promote.py main` verifies an RC before merging to main. Helpers reuse tested artifacts and allow exact-tree merges. Resolve conflicts on dev; never bypass branch protection.

## Signing and scope

Phone/tablet and [TV](GOOGLE_TV.md) APKs share signing credentials and provenance. Validate debug identities and physical devices. See [signing](ANDROID_SIGNING.md), [release notes](RELEASE_NOTES.md) and [delivery](CI_CD.md).

## Application identity

The AlreadyOn TV rename preserves application IDs and signing. Older apps may need one manual signed APK update over the existing installation; preserve data. See [identity](ORGANIZATION.md).
