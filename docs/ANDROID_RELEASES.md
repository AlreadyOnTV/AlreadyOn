# Android APK releases

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

## Release lifecycle

Use the active dated branch and [development setup](DEVELOPMENT.md). Test at <https://localhost:5173>; open its dev PR after approval.

| Promotion | Artifact tag | Release title | Publication |
| --- | --- | --- | --- |
| Validated `dev` source | `YY.MM.DD-nightly` | `YY.MM.DD-nightly` | Signed APKs; image `nightly` |
| Tested `dev` source into `test` | `YY.MM.DD-rcN` | `YY.MM.DD-rcN` | Image `latest`; freshly built RC APKs |
| Current `test` into `main` | `YY.MM.DD-stable` | `YY.MM.DD-stable` | Image `stable`; freshly built Stable APKs |

Each new release builds and signs both APKs with its target-stage version name and a strictly higher version code. RC builds use the tested source; Stable defaults to the newest published RC's exact source commit, independently of the workflow commit. A manual `source_sha` override must identify validated source in the selected branch history. Docker promotions reuse the tested digest without rebuilding.

After nightly acceptance, dispatch **Android Nightly, RC and Stable** on `test` (`promote-test`) or `main` (`stable`). Completed reruns verify published assets. `repair-stable` explicitly replaces historical copied-candidate Stable APKs with fresh Stable builds at the same source/tag; correctly versioned published APKs remain immutable. Stable locks the date.

An approved direct RC can instead use **Build Date Release Image** on dev with `publication=rc-candidate`, an exact revision and date. Full validation and image attestations precede a signed draft RC; existing nightly artifacts/channels remain unchanged. The normal promotion helper accepts this matching draft and tested image before advancing test. Test builds fresh RC APKs with higher codes and publishes the matching draft.

Updated phone and TV builds use the Admin-selected Stable (default), Release Candidate or Nightly cycle, not an installed-label track lock. Existing APKs need a signed update to gain the setting or nightly recognition. See [update policy and existing-client limitations](ANDROID_UPDATES.md).

## Manual stage promotion

After nightly acceptance, run `python3 scripts/promote.py test YY.MM.DD` to promote its tested source and image digest. The test branch publishes the first `YY.MM.DD-rc1` APKs; later candidates increment the suffix. After RC acceptance, `python3 scripts/promote.py main` selects the current test head. Both branches build fresh target-stage APKs with higher version codes. Weekly automation never moves test/main. Retire dated development branches only after delivery and explicit cleanup approval.

## Artifacts and gates

The date tag advances after dev validation. [Generated changelogs](RELEASE_NOTES.md) compare predecessor tags; promotions preserve predecessor known issues.

Releases attach phone/tablet and TV APKs, `SHA256SUMS`, `android-release.json`, three inventory-only CycloneDX SBOMs and `sbom-manifest.json`. Inventories bind the image digest, signed APK hashes and source. Promotions retain signing identity and source provenance while rebuilding APK versions. Titles never rename artifacts. Older releases without inventories remain compatible.

Date PRs require quality, CodeQL, full Go/browser tests, packaged-runtime and security checks. [Promotion helpers](CI_CD.md) verify provenance and exact trees. Review [Stable readiness](STABLE_READINESS.md); preserve branch protection and live data.

See [signing](ANDROID_SIGNING.md), [TV](GOOGLE_TV.md) and [identity](ORGANIZATION.md). Existing application IDs/signing remain compatible; older updaters may require one manual signed installation preserving data.
