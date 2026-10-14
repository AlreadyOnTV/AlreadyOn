# Dependency maintenance

Weekly Dependency Review runs Monday at 03:17 America/New_York against `dev`, or on manual request. It reads published runtime/CI images, records immutable digests, source revision, lock/manifest checksums, Ubuntu and FFmpeg/ffprobe versions, then scans source and both images. Baseline metadata is retained for 90 days. Raw vulnerability/secret reports stay private to the ephemeral runner; public summaries contain counts only.

Dependabot proposes weekly Go, npm, Android/Gradle and GitHub Actions updates against `dev`. Review their changes into the active dated release branch; do not merge dependency PRs directly into dev. Dependency proposals must update associated documentation and pass the release PR gates. Version proposals are not proof that every upstream advisory is fixed. Security discovery also uses the pinned scanner and reviewed exception policy. See [scanning](VULNERABILITY_SCANNING.md).

The existing daily cleanup workflow has an independent read-only watchdog. It fails when no successful scheduled weekly review exists, its completion is more than eight days old, or timestamps are in the future. Manual runs cannot conceal missed schedules. Configure GitHub workflow notifications for failures. A stopped GitHub scheduler can also stop the watchdog: inspect Actions history independently during release review rather than assuming either schedule ran. The initial deployment will fail health checks until its first successful scheduled review.

No scheduled job publishes or deploys application/base images. Maintainers refresh bases explicitly. Reviewed pin changes and refreshed bases enter the ordinary dated dev pipeline: quality, Go/browser tests, packaged-runtime checks and scanning precede image publication. Test/main reuse the recorded tested digest. Never rebuild a released image merely to silence an advisory; prepare a new validated candidate.

Weekly automation and Dependabot configuration must reach the repository default branch before schedules become active. Before closing maintenance delivery, verify one successful scheduled review, a real dependency proposal, and watchdog rejection of a synthetic missed-run response. Local unit tests establish detection logic, not scheduler operation.

## October 18 dependency batch

Dependabot #43–#59 update Go, Android libraries/tooling, Pages actions v5, Android setup v4 and locked npm tooling. #56/#58 duplicate one Media3 update. Regenerate combined locks.

Gradle 9.8.1 requires AGP 9.4.1 with built-in Kotlin and TV Kotlin source sets; [AGP 8 uses a removed API](https://docs.gradle.org/9.8.1/userguide/upgrading_version_9.html#agp_8x_incompatible). OkHttp 5 requires compile SDK 37; target SDK 36, minimum SDK 26 and identities remain unchanged. Validate inventory and both debug/release apps.

Add required infrastructure documentation. Test the replacement npm CLI in the rebuilt CI base; dependency resolution alone cannot establish compatibility.
