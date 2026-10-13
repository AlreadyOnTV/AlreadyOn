# Android APK releases

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

## Release lifecycle

Use the active dated branch in one checkout; see [development setup](DEVELOPMENT.md). Test at <https://localhost:5173> and open its dev PR only when validation is approved.

| Promotion | Release tag | Publication |
| --- | --- | --- |
| Merged date PR into `dev` | `YYYY-MM-DD-alphaN` | Fresh signed APKs and image tagged `nightly` plus `YYYY-MM-DD` |
| Merge tested date commit from `dev` to `test` | `YYYY-MM-DD-RCN` | Retag image `latest`; publish/copy tested APK bytes |
| Merge current `test` release into `main` | `YYYY-MM-DD-Stable` | Retag image `stable`; publish the latest RC APK bytes |

RC1 reuses alpha APK bytes. Later dev hotfixes build a draft for the next RC; test promotion publishes it. RC changelogs include only marked app notes since the prior stage. Stable reuses the latest RC APKs and locks the date.

The date tag advances after dev validation. Alpha changelogs include marked Android/TV notes since the latest earlier-date Android release, excluding server-only notes. Promotions reuse the tested image digest and signed APKs; no rebuild/re-sign occurs.

Each new release attaches both APKs, `SHA256SUMS`, `android-release.json`, three inventory-only CycloneDX SBOMs and `sbom-manifest.json`. Inventories bind the runtime digest and signed APK hashes to the source; Android provenance binds their manifest. Promotions preserve these bytes. Older releases without inventories remain compatible. Promotions retain the APK `versionName`/`versionCode`; titles do not rename tags.

Manual candidates require a recorded image digest for their exact source.

## Automation and gates

Review the [Stable readiness checklist](STABLE_READINESS.md) before requesting main promotion.

Date PRs run quality, CodeQL and full Go/browser e2e. Dev merge validation publishes the date image and alpha APKs, advances the date tag, then removes the merged date branch. `python3 scripts/promote.py test YYYY-MM-DD` verifies image/alpha APK provenance before merging to test; `python3 scripts/promote.py main` verifies an RC before merging to main. The helpers create exact-tree merge commits when branches have diverged, reuse tested artifacts and skip duplicate e2e. Conflicts stop for resolution on dev. If branch protection rejects a push, stop; never bypass it.

## Signing and scope

Phone/tablet and [TV](GOOGLE_TV.md) APKs share permanent signing credentials and provenance. Validate debug identities and physical devices. See [signing](ANDROID_SIGNING.md), [release notes](RELEASE_NOTES.md) and [delivery](CI_CD.md).

## Application identity

The organization-owned application ID differs from older previews, which cannot update in place. Uninstall the earlier preview before installing this version; app-local settings are not transferred.
