# Signed artifact evidence

Manual Nightly publication uploads the validated image under a temporary run tag, resolves its immutable digest, and signs build provenance and an inventory-only CycloneDX SBOM using GitHub OIDC. The inventory is reconstructed from allowlisted package fields in the exact candidate scan; raw findings, credentials and filesystem paths are not published. Coverage is detected runtime OS/application packages, not proof that every statically bundled dependency is identified.

Both signatures must verify for the repository, hosted runner and release builder before `nightly` or the date tag moves. Verification checks the exact source commit. A signing/verification failure leaves channel tags unchanged; its staged image remains eligible for cleanup after the run finishes. Image layers are never rebuilt to create attestations.

Stable publication verifies and reuses the same signed Nightly image digest before adding `latest`. OCI revision labels and unsigned Android manifests alone are not signed provenance. Android APK inventories bind each fresh signed APK to its source and image digest; see [Android policy](ANDROID_RELEASES.md).

Verify by immutable digest with:

```sh
gh attestation verify oci://ghcr.io/alreadyontv/alreadyon@sha256:DIGEST \
  --repo AlreadyOnTV/AlreadyOn \
  --signer-workflow AlreadyOnTV/AlreadyOn/.github/workflows/rebuild.yml \
  --deny-self-hosted-runners
```

Repeat with `--predicate-type https://cyclonedx.org/bom` for the signed SBOM. Signatures/SBOM predicates are stored in GitHub's attestation service, independently of OCI manifest cleanup. A downloadable inventory workflow artifact lasts 90 days. The immutable image digest is the snapshot identifier; mutable date/channel tags are aliases, not independent immutable releases.

[Retention](IMAGE_RETENTION.md) protects channel digests, dates for 90 days, and nested manifests. This does not promise indefinite rollback storage or database recovery. Verify digest availability before rollback/promotion. Read the [official attestation contract](https://github.com/actions/attest).

Hosted acceptance requires successful signed Nightly publication, consumer verification of both predicates, and Stable publication preserving its digest. Local policy tests cannot establish those external delivery gates.
