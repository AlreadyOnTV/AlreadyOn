# Dependency maintenance

Dependabot checks Go, npm, Android/Gradle and GitHub Actions every Monday at 03:00 America/New_York, grouping updates per ecosystem and targeting `dev`. Each resulting PR triggers CodeQL, dependency review, Android checks and the full E2E/runtime suite; auto-merge waits for all gates. Dependency pin/lock changes need no prose unless they change a public contract; other source changes retain documentation requirements.

For npm tooling advisories, use reviewed package-scoped overrides when upstream parent ranges still require a vulnerable major. Regenerate the lock through npm, verify the installed override versions, run `npm audit`, and build the CI base; do not hand-edit nested lock entries.

The dependency-review gate blocks moderate-or-higher advisories. PR validation uses isolated fixtures, read-only candidate credentials and disposable packaged-runtime checks; publication credentials are unavailable. Critical/High findings, secret matches and scanner failures block delivery. Unfixed upstream findings require repair or a reviewed exception, not automatic dismissal.

Dependabot PRs merge into `dev` only after all PR gates pass. A later `dev`-to-`main` PR uses the same checks. Nightly publication is a separate manual action from `dev` or `main`; same-date reruns replace assets only after publication gates pass and allocate higher APK codes.

Stable publication is a separate manual workflow on `main`. No weekly job publishes releases, deploys, refreshes base images merely to hide findings, or dismisses alerts. Stable reuses the latest validated Nightly image digest and builds fresh APKs with the same source and permanent signing identity.

The weekly Maintenance workflow combines a read-only scan of the published Nightly/CI images with registry and GitHub-release retention. It records image digests, source, manifests and tool versions for 90 days; raw secret/vulnerability reports stay private to the ephemeral runner. Cleanup keeps active Nightly/Stable channels, all dated artifacts within 90 days, and the five newest expired dated releases total; drafts and unknown release tags are left untouched. Scheduled cleanup applies this policy; manual dispatch is a dry run unless explicitly confirmed.

Dependabot configuration and the Maintenance workflow must reach the default branch before schedules activate. Verify an actual scheduled PR and cleanup plan; local policy tests do not establish scheduler operation. Toolchain and base-image upgrades still require compatible locks and builds. See [scanning](VULNERABILITY_SCANNING.md), [delivery](CI_CD.md) and [Android releases](ANDROID_RELEASES.md).
