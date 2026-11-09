# Images and retention

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

Registry tags identify approved image digests; they are separate from GitHub release tags:

- `nightly`: the latest manually validated Nightly build. `YY.MM.DD-nightly` and `YY.MM.DD` identify that build date.
- `latest`: the image digest of the Nightly selected for Stable APK publication.

Manual Nightly publication from `dev` or `main` publishes `nightly`, `YY.MM.DD-nightly` and `YY.MM.DD`. A validated same-date rerun may replace those aliases and APK assets with higher version codes and updated provenance. Manual Stable publication retags the selected Nightly digest as `latest`; it does not rebuild the image. See [release policy](ANDROID_RELEASES.md).

Stable publication reuses the Nightly image; it does not rebuild source under `latest`. Record source commits and immutable digests before publishing. Mutable tags alone are insufficient to identify an artifact.

Local base images are refreshed on demand when their build timestamp exceeds seven days or their recipe/parent no longer matches. See [delivery](CI_CD.md).

Weekly Cleanup preserves active Nightly/Stable digests and all dated images/releases within 90 days. Beyond 90 days it keeps the five newest image versions total, the five newest APK releases per stage, and five source-date tags total. Drafts, unknown tags and channel-referenced manifests are preserved. Closed-PR caches are cleaned separately. Daily Build Base Image removes superseded base versions after 30 days while protecting active aliases and manifest descendants. Retention is not a database backup policy.

Store private deployment records and registry credentials outside Git. New-cycle image provenance and SBOM signatures are stored independently in GitHub attestations; see [artifact evidence](ARTIFACT_PROVENANCE.md).
