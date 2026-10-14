# Stable readiness checklist

Promotion into `main` is the maintainer’s release decision and triggers Stable publication. This checklist consolidates existing review evidence; it does not add a hidden publication approval or authorize a promotion.

- [ ] Select the tested release source and confirm the exact-tree/ancestry checks for dev → test → main. Resolve conflicts on dev, preserve the tested image and APK bytes, and record the release date, commit and immutable digest.
- [ ] Confirm required hosted quality, documentation, syntax, CodeQL, Go/browser regressions, packaged-runtime checks and vulnerability gates passed for the candidate. Review Critical/High findings and approved exceptions. Local checks alone do not establish hosted acceptance.
- [ ] Verify the image revision/digest, phone/TV APK checksums, package/target, version and permanent signing certificate. Verify inventory/provenance required by that cycle. New cycles require signed image evidence; do not invent evidence for older artifacts. See [artifact evidence](ARTIFACT_PROVENANCE.md).
- [ ] Record tested platforms, resource expectations and known limitations. Review physical phone/TV acceptance, discovery, updates, remote navigation and sustained playback. Do not imply universal codec/GPU/device validation. Apple clients are not planned; AMD/NVIDIA hardware validation and a comprehensive compatibility matrix are not promised.
- [ ] Review migration/API compatibility and synthetic upgrade/restart evidence. Confirm accounts, settings, library and schedules persist as intended, with intentional session changes documented. Preserve live databases/media; never use production data as destructive fixtures.
- [ ] Agree on recovery or fix-forward handling. Confirm any selected prior digest remains available and that database/schema compatibility permits the proposed recovery. Image retention is not a database recovery plan; do not assume downgrading a container safely downgrades its database. See [retention](IMAGE_RETENTION.md).
- [ ] Ensure installation, license/attribution and support guidance match shipped behavior. Verify [release titles/sections](RELEASE_NOTES.md), linked bugfixes, known issues and empty-section messages. Track unresolved redistribution review and supported-platform/minimum-resource documentation explicitly; a checklist or published Stable tag does not complete those tasks.
- [ ] Route user feedback to [public issues](https://github.com/AlreadyOnTV/AlreadyOn/issues) and vulnerabilities through [private reporting](../SECURITY.md). Describe supported-version limits and known installation/security prompts.
- [ ] After authorized promotion, verify published channel digests and release assets against the accepted candidate. Record actual results; do not claim deployment or device verification from publication alone.

See [delivery](CI_CD.md) and [Android releases](ANDROID_RELEASES.md).
