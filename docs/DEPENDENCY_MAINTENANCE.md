# Dependency maintenance

Dependabot groups weekly Go, npm, Android/Gradle and GitHub Actions updates every Monday at 03:00 America/New_York, targeting `dev`. PRs auto-merge after CodeQL, dependency review, Android and full E2E/runtime gates pass. GitHub security-update PRs target the default branch and do not honor `target-branch`. Security alerts and security updates remain enabled. **Route Dependabot PRs to dev** retargets same-repository bot PRs after CodeQL, with hourly fallback and manual dispatch. Retargeting reruns all validation gates; title/body edits do not. Dependabot PRs to `main` cannot auto-merge ([GitHub options](https://docs.github.com/en/code-security/reference/supply-chain-security/dependabot-options-reference)).

Install the private routing GitHub App only on this repository with pull-request write and mandatory metadata read permissions. Configure repository variable `DEPENDABOT_ROUTER_APP_ID` and Actions secret `DEPENDABOT_ROUTER_PRIVATE_KEY`; never copy the key into Dependabot secrets or PR jobs. The router uses a short-lived repository-scoped token and fresh metadata to change eligible bases. It never checks out PR code or consumes workflow artifacts; existing PR gates perform merges. Routing activates after the workflow reaches `main`; missing credentials fail visibly.

Dependency pin/lock changes need no prose unless they change a public contract; action pins may retain inline version comments; other source changes retain documentation requirements. Android PR tasks retry repository HTTP 429 throttling up to three attempts with 20- and 40-second delays; other failures stop immediately.

For npm tooling advisories, use reviewed package-scoped overrides when parent ranges require a vulnerable major. Regenerate the lock through npm, verify the installed override versions, run `npm audit`, and build the CI base; do not hand-edit nested lock entries.

The dependency-review gate blocks moderate-or-higher advisories. PR validation uses isolated fixtures, read-only candidate credentials and disposable packaged-runtime checks; publication credentials are unavailable. Critical/High findings, secret matches and scanner failures block delivery. Unfixed upstream findings require repair or a reviewed exception, not automatic dismissal.

Dependabot PRs merge into `dev` only after all PR gates pass. A later `dev`-to-`main` PR uses the same checks. Nightly publication is a separate manual action from `dev` or `main`; same-date reruns replace assets only after publication gates pass and allocate higher APK codes.

Stable publication is a separate manual workflow on `main`. No weekly job publishes releases, deploys, refreshes base images merely to hide findings, or dismisses alerts. Stable reuses the latest validated Nightly image digest and builds fresh APKs with the same source and permanent signing identity.

Security Scan covers the final published runtime image and only phone/TV `releaseRuntimeClasspath` dependencies; it excludes CI, compiler and test toolsets. Weekly Cleanup resolves the current Nightly digest and revision from the same pulled image reference, runs that scan, then retains image versions and release assets/tags within 90 days plus the five newest expired releases per stage. Drafts and unknown release tags are left untouched. Manual cleanup dispatch is a dry run unless explicitly confirmed.

Dependabot configuration, Weekly Cleanup and Daily Build Base Image must reach the default branch before schedules activate. Verify a real scheduled PR and cleanup plan; local policy tests do not establish scheduler operation. Upgrades require compatible locks and builds. See [scanning](VULNERABILITY_SCANNING.md), [delivery](CI_CD.md) and [Android releases](ANDROID_RELEASES.md).
