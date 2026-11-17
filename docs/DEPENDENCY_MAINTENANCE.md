# Dependency maintenance

Dependabot checks Go, npm, Android/Gradle and GitHub Actions every Monday at 03:00 America/New_York, grouping updates per ecosystem and targeting `dev`. Each resulting PR triggers CodeQL, dependency review, Android checks and the full E2E/runtime suite; auto-merge waits for all gates. GitHub security-update PRs target the default branch and do not honor `target-branch`. Keep security alerts and security updates enabled; security-fix PRs opened against `main` must be retargeted to `dev` before automatic merge. Retargeting reruns all validation gates; title/body edits do not. Dependabot PRs to `main` cannot auto-merge. Automatic retargeting requires an integration credential that can trigger subsequent PR workflows ([GitHub options](https://docs.github.com/en/code-security/reference/supply-chain-security/dependabot-options-reference)).

Dependency pin/lock changes need no prose unless they change a public contract; action pins may retain inline version comments; other source changes retain documentation requirements. Android PR tasks retry repository HTTP 429 throttling up to three attempts with 20- and 40-second delays; other failures stop immediately.

For npm tooling advisories, use reviewed package-scoped overrides when upstream parent ranges still require a vulnerable major. Regenerate the lock through npm, verify the installed override versions, run `npm audit`, and build the CI base; do not hand-edit nested lock entries.

The dependency-review gate blocks moderate-or-higher advisories. PR validation uses isolated fixtures, read-only candidate credentials and disposable packaged-runtime checks; publication credentials are unavailable. Critical/High findings, secret matches and scanner failures block delivery. Unfixed upstream findings require repair or a reviewed exception, not automatic dismissal.

Dependabot PRs merge into `dev` only after all PR gates pass. A later `dev`-to-`main` PR uses the same checks. Nightly publication is a separate manual action from `dev` or `main`; same-date reruns replace assets only after publication gates pass and allocate higher APK codes.

Stable publication is a separate manual workflow on `main`. No weekly job publishes releases, deploys, refreshes base images merely to hide findings, or dismisses alerts. Stable reuses the latest validated Nightly image digest and builds fresh APKs with the same source and permanent signing identity.

Security Scan covers the final published runtime image and only phone/TV `releaseRuntimeClasspath` dependencies; it excludes CI, compiler and test toolsets. Weekly Cleanup resolves the current Nightly digest and revision from the same pulled image reference, runs that scan, then retains image versions and release assets/tags within 90 days plus the five newest expired releases per stage. Drafts and unknown release tags are left untouched. Manual cleanup dispatch is a dry run unless explicitly confirmed.

Dependabot configuration, Weekly Cleanup and Daily Build Base Image must reach the default branch before schedules activate. Verify a real scheduled PR and cleanup plan; local policy tests do not establish scheduler operation. Toolchain and base-image upgrades still require compatible locks and builds. See [scanning](VULNERABILITY_SCANNING.md), [delivery](CI_CD.md) and [Android releases](ANDROID_RELEASES.md).
