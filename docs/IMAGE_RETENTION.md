# Images and retention

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

Registry tags identify approved image digests; they are separate from GitHub release tags:

- `nightly`: the latest validated dev build. `YY.MM.DD-nightly` and its `YY.MM.DD` alias identify that date's immutable nightly.
- `latest`: an approved test promotion.
- `stable`: an approved main promotion.

Validated dev work publishes `nightly`, `YY.MM.DD-nightly` and `YY.MM.DD`; a different source cannot overwrite a published dated nightly. Test/main promotions merge the tested tree and retag the same recorded digest as `latest`/`stable`; they do not rebuild. See [release policy](ANDROID_RELEASES.md).

Promotion reuses the tested image; it does not rebuild source under a new tag. Record the source commit and immutable digest before publishing. Mutable tags alone are insufficient to identify a deployed artifact.

Local base images are refreshed on demand when their build timestamp exceeds seven days or their recipe/parent no longer matches. See [delivery](CI_CD.md).

Cleanup must preserve active release digests and anything referenced by a supported tag. PR images and unreferenced intermediates can be removed according to the registry cleanup policy. Check references before deleting artifacts; retention is not a database backup policy.

Store private deployment records and registry credentials outside Git. New-cycle image provenance and SBOM signatures are stored independently in GitHub attestations; see [artifact evidence](ARTIFACT_PROVENANCE.md).
