# Images and retention

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

Registry tags identify approved image digests; they are separate from GitHub release tags:

- `nightly`: the latest validated main build. `YY.MM.DD-nightly` and `YY.MM.DD` identify that build date.
- `latest`: the image digest of the Nightly selected for Stable APK publication.

Validated main merges publish `nightly`, `YY.MM.DD-nightly` and `YY.MM.DD`. A later validated main merge may replace the same-date aliases and APK release through the workflow, using higher APK version codes and updated provenance. Manual Stable publication retags the selected Nightly digest as `latest`; it does not rebuild the image. See [release policy](ANDROID_RELEASES.md).

Stable publication reuses the Nightly image; it does not rebuild source under `latest`. Record source commits and immutable digests before publishing. Mutable tags alone are insufficient to identify an artifact.

Local base images are refreshed on demand when their build timestamp exceeds seven days or their recipe/parent no longer matches. See [delivery](CI_CD.md).

Cleanup must preserve active release digests and anything referenced by a supported tag. PR images and unreferenced intermediates can be removed according to the registry cleanup policy. Check references before deleting artifacts; retention is not a database backup policy.

Store private deployment records and registry credentials outside Git. New-cycle image provenance and SBOM signatures are stored independently in GitHub attestations; see [artifact evidence](ARTIFACT_PROVENANCE.md).
