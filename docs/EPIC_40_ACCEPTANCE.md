# Containers and automated releases acceptance

The 2026-10-18 review reconciles the current implementation with historical container/release tickets. Earlier Catbug-runner, DevContainer, separate-listener and rebuild-on-promotion descriptions are superseded.

| Task | Current evidence and status |
| --- | --- |
| 41: Runtime image | Shared Ubuntu 26.04 baseline, patched FFmpeg/ffprobe, non-root entrypoint identity, read-only media defaults, recorded codec/build metadata and revision labels. Complete. |
| 42: Image integration | `tests/integration/container.py` runs against the candidate: non-root, permissions, TLS, restart persistence, synthetic playback and scoped/revoked media authorization. Complete. |
| 43: Dev publication | Hosted dev validation includes quality, Go/browser tests, runtime integration and scanning before publishing. The registry moved to `ghcr.io/alreadyontv/alreadyon`; Catbug is deployment-only. Complete. |
| 44: Latest/stable | Hosted promotions reuse the tested digest, without rebuilding or changing its date alias. Stable publication is now verified. Complete. |
| 45: Maintenance | Weekly dependency proposals, read-only source/runtime/CI security review, digest/version/lock baseline and independent daily missed-schedule detection are implemented and locally tested. Hosted activation/acceptance remains open. |
| 46: Artifact evidence | Runtime/Android SBOM publication and scan policy already exist. New image provenance/SBOM signatures, consumer verification and pre-channel signing gates are implemented. Real hosted signed publication/verification remains open. |
| 138: Local container path | Rootless Podman image build and isolated runtime harness exist; native development replaces DevContainer. Existing hosted candidate/runtime results confirm shared packaging. Complete. |
| 172: Deployment configuration | PUID/PGID, GPU groups, writable config/scratch and read-only media documented. Historical Catbug delivery is recorded; this review does not redeploy or certify current host state. Complete. |
| 151: Ubuntu upgrade | Prior issue records 26.04 regression/deployment evidence and explicit GPU/client limitations. DevContainer scope was subsequently retired. Unavailable hardware was never claimed passed. Complete. |

Current hosted [dev build](https://github.com/AlreadyOnTV/AlreadyOn/actions/runs/38304610827) and [stable promotion](https://github.com/AlreadyOnTV/AlreadyOn/actions/runs/38326511902) succeeded. On 2026-10-13 registry inspection confirms nightly/latest/stable all use source `83111a5e424ff1415ffb494076570d2ad0f3d741` and digest `sha256:101d9f3cae30516b988880b0f2a86f39a69a86ad1b010aa1c1d534b2e87d762d`. Stable release assets include phone/TV APKs, checksums and three inventory SBOMs.

See [maintenance](DEPENDENCY_MAINTENANCE.md), [signed evidence](ARTIFACT_PROVENANCE.md), [delivery](CI_CD.md) and [retention](IMAGE_RETENTION.md). New implementation does not imply hosted validation or publication. Keep 45/46 open until their external acceptance gates have evidence.
