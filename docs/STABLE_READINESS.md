# Stable readiness checklist

Merging a validated PR to `main` publishes Nightly. Manual Stable publication is a separate maintainer action that reuses the latest Nightly image and builds fresh Stable APKs.

- [ ] Confirm main PR CodeQL, dependency review, full Go/browser/runtime tests, Android checks and vulnerability gates passed. Review Critical/High findings and approved exceptions.
- [ ] Verify the Nightly image revision/digest, phone/TV APK checksums, package/target, version and permanent signing certificate. Verify signed image provenance and inventory. See [artifact evidence](ARTIFACT_PROVENANCE.md).
- [ ] Confirm Stable will use the latest validated Nightly source. The image is retagged as `latest` without rebuilding; both Stable APKs are freshly signed with higher version codes.
- [ ] Record tested platforms, resource expectations and known limitations. Review physical phone/TV acceptance, discovery, updates, remote navigation and sustained playback. Do not imply universal codec/GPU/device validation. Apple clients are not planned; AMD/NVIDIA hardware validation and a comprehensive compatibility matrix are not promised.
- [ ] Review migration/API compatibility and synthetic upgrade/restart evidence. Confirm accounts, settings, library and schedules persist as intended, with intentional session changes documented. Preserve live databases/media; never use production data as destructive fixtures.
- [ ] Agree on recovery or fix-forward handling. Confirm any selected prior digest remains available and that database/schema compatibility permits the proposed recovery. Image retention is not a database recovery plan; do not assume downgrading a container safely downgrades its database. See [retention](IMAGE_RETENTION.md).
- [ ] Ensure installation, license/attribution and support guidance match shipped behavior. Verify [release titles/sections](RELEASE_NOTES.md), linked bugfixes, known issues and empty-section messages. Track unresolved redistribution and supported-platform documentation explicitly.
- [ ] Route user feedback to [public issues](https://github.com/AlreadyOnTV/AlreadyOn/issues) and vulnerabilities through [private reporting](../SECURITY.md). Describe supported-version limits and known installation/security prompts.
- [ ] Verify `nightly` and `latest` resolve to the same image digest and Stable APK manifests name the same source. Record actual results; do not claim deployment or device verification from publication alone.

See [delivery](CI_CD.md) and [Android releases](ANDROID_RELEASES.md).
