# Android APK releases

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

## Release lifecycle

Use the active dated branch and [development setup](DEVELOPMENT.md). Test at <https://localhost:5173>; open its dev PR after approval.

| Promotion | Artifact tag | Release title | Publication |
| --- | --- | --- | --- |
| Dated PR into `dev` | `YY.MM.DD-alphaN` | `YY.MM.DD-alphaN` | Signed APKs; image `nightly` and date tags |
| Tested `dev` source into `test` | `YY.MM.DD-RCN` | `YY.MM.DD-rcN` | Image `latest`; exact tested APKs |
| Current `test` into `main` | `YY.MM.DD-Stable` | `YY.MM.DD-stable` | Image `stable`; latest RC APKs |

RC1 normally reuses alpha APKs. Approved direct RC1 builds require an unpublished draft targeting the exact dev source. Replacing RC1 requires explicit approval, removing its release/tag, and reserving a replacement draft with a new version code. Later hotfixes draft the next RC. Stable locks the date.

Future phone and TV builds must offer newer alpha, RC and stable updates even when the installed release is Stable. Stable publication must not lock clients to stable-only updates. See [update policy and existing-client limitations](ANDROID_UPDATES.md).

## Approved direct Stable

Dispatch **Build Date Release Image** on `dev` with `publication=stable`. Full validation and signed-image gates precede building/signing both Stable APKs once. Only the Stable release/tag is allocated; no alpha or RC is created.

Reconcile historical promotion ancestry on the dated branch before its dev PR. After publication, fast-forward `test` and `main` to the identical validated dev commit; their Android workflows verify existing Stable assets without rebuilding or creating an RC. Cancel the automatic candidate build before publication when selecting this path. Retire the retained dated branch after delivery verification.

## Artifacts and gates

The date tag advances after dev validation. [Generated changelogs](RELEASE_NOTES.md) compare predecessor tags; identical-artifact promotions preserve tested descriptions.

Releases attach phone/tablet and TV APKs, `SHA256SUMS`, `android-release.json`, three inventory-only CycloneDX SBOMs and `sbom-manifest.json`. Inventories bind the image digest, signed APK hashes and source. Promotions preserve bytes, signing identity and embedded versions; titles never rename artifacts. Older releases without inventories remain compatible.

Date PRs require quality, CodeQL, full Go/browser tests, packaged-runtime and security checks. [Promotion helpers](CI_CD.md) verify provenance and exact trees. Review [Stable readiness](STABLE_READINESS.md); preserve branch protection and live data.

See [signing](ANDROID_SIGNING.md), [TV](GOOGLE_TV.md) and [identity](ORGANIZATION.md). Existing application IDs/signing remain compatible; older updaters may require one manual signed installation preserving data.
