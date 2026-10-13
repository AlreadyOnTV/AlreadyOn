# Images and retention

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

Registry tags identify approved image digests; they are separate from GitHub release tags:

- `nightly` and `YYYY-MM-DD`: the active date release from `dev`; validated hotfixes move the date digest until Stable.
- `latest`: an approved test promotion.
- `stable`: an approved main promotion.

Every merged date PR builds and validates a fresh dev image, publishing `nightly` and `YYYY-MM-DD`. Test/main promotions merge the tested tree and retag the same recorded digest as `latest`/`stable`; they do not rebuild. See [release policy](ANDROID_RELEASES.md).

Promotion reuses the tested image; it does not rebuild source under a new tag. Record the source commit and immutable digest before publishing. Mutable tags alone are insufficient to identify a deployed artifact.

Local base images are refreshed on demand when their build timestamp exceeds seven days or their recipe/parent no longer matches. See [delivery](CI_CD.md).

Cleanup must preserve active release digests and anything referenced by a supported tag. PR images and unreferenced intermediates can be removed according to the registry cleanup policy. Check references before deleting artifacts; retention is not a database backup policy.

Store private deployment records and registry credentials outside Git. Future supply-chain work, including provenance and SBOM publication, is tracked separately.
