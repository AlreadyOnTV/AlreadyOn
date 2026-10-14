# Signed artifact evidence

Starting with cycle `2026-10-18`, dev publication uploads the already tested image under a temporary run tag, resolves its immutable digest, and signs build provenance and an inventory-only CycloneDX SBOM using GitHub OIDC. The inventory is reconstructed from allowlisted package fields in the exact candidate scan; raw findings, credentials and filesystem paths are not published. Coverage is detected runtime OS/application packages, not proof that every statically bundled dependency is identified.

Both signatures must verify for the repository, hosted runner and release builder before `nightly` or the date tag moves. Dev verification also checks the exact source commit. A signing/verification failure leaves channel tags unchanged; its staged image remains eligible for cleanup after the run finishes. Image layers are never rebuilt to create attestations.

Test/main verify the same signed image digest before retagging. Earlier cycles retain their original promotion policy; no retrospective attestations are invented. OCI revision labels and unsigned Android manifests alone are not signed provenance. These changes attest the application image; Android APK provenance remains separately described in [Android policy](ANDROID_RELEASES.md).

Verify by immutable digest with:

```sh
gh attestation verify oci://ghcr.io/alreadyontv/alreadyon@sha256:DIGEST \
  --repo AlreadyOnTV/AlreadyOn \
  --signer-workflow AlreadyOnTV/AlreadyOn/.github/workflows/rebuild.yml \
  --deny-self-hosted-runners
```

Repeat with `--predicate-type https://cyclonedx.org/bom` for the signed SBOM. Signatures/SBOM predicates are stored in GitHub's attestation service, independently of OCI manifest cleanup. A downloadable inventory workflow artifact lasts 90 days. The immutable image digest is the snapshot identifier; mutable date/channel tags are aliases, not independent immutable releases.

[Retention](IMAGE_RETENTION.md) protects channel digests, dates for 90 days, and nested manifests. This does not promise indefinite rollback storage or database recovery. Verify digest availability before rollback/promotion. Read the [official attestation contract](https://github.com/actions/attest).

Hosted acceptance requires a successful signed dev publication, consumer verification of both predicates, and promotion preserving its digest. Local policy tests cannot establish those external delivery gates.
