# Android APK releases

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

## Release lifecycle

Develop on `dev`, test at <https://localhost:5173>, then open a PR to `main` after review.

| Event | Artifact tag | Publication |
| --- | --- | --- |
| Manual Nightly publication from `dev` or `main` | `YY.MM.DD-nightly` | Signed APKs; image `nightly` and date aliases |
| Manual Stable publication from latest Nightly | `YY.MM.DD-stable` | Fresh signed APKs; image alias `latest` reuses the Nightly digest |

Each release builds/signs both APKs with a strictly higher version code. Stable selects the newest published Nightly source in main history. Docker `latest` reuses that source's verified image digest; it does not rebuild the image. A same-date rerun replaces the release/tag only after all gates pass, and writes new source-bound APK manifests/checksums.

Run Nightly publication manually from `dev` or `main` only after that source has passed PR validation. A Nightly published from `dev` becomes eligible for Stable after its exact source reaches `main`. After accepting a Nightly, run **Build and Publish Release** with `operation: stable` on `main`; it verifies the source/digest, builds fresh higher-code Stable APKs with the permanent signing identity, then moves `latest` to the same image digest.

Updated phone and TV builds use the Admin-selected Stable (default) or Nightly cycle, not an installed-label track lock. Stored legacy RC preferences fall back to Stable. Existing APKs need a signed update to gain the setting. See [update policy and existing-client limitations](ANDROID_UPDATES.md).

## Manual stage promotion

Nightly and Stable publication are manual. Nightly may select `dev` or `main`; Stable selects the latest validated Nightly reachable from `main`. Weekly automation does not publish releases or deploy builds.

## Artifacts and gates

Dates are generated in `America/New_York`. [Generated changelogs](RELEASE_NOTES.md) compare predecessor tags and preserve known issues.

Releases attach phone/tablet and TV APKs, `SHA256SUMS`, `android-release.json`, three inventory-only CycloneDX SBOMs and `sbom-manifest.json`. Inventories bind image digest, signed APK hashes and source. Replacement builds retain signing identity and regenerate source provenance. Older releases without inventories remain compatible.

Main PRs require quality, CodeQL, full Go/browser tests, packaged-runtime and security checks. Review [Stable readiness](STABLE_READINESS.md); preserve branch protection and live data.

See [signing](ANDROID_SIGNING.md), [TV](GOOGLE_TV.md) and [identity](ORGANIZATION.md). Existing application IDs/signing remain compatible; older updaters may require one manual signed installation preserving data.
