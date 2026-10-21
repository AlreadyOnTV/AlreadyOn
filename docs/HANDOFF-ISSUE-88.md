# Handoff: issue #88 and alpha2 delivery

Snapshot: 2026-10-20. Continue in the primary checkout on branch `26.10.25`.

## Objective and authorization

Implement [issue #88](https://github.com/AlreadyOnTV/AlreadyOn/issues/88), add it to the existing [PR #87](https://github.com/AlreadyOnTV/AlreadyOn/pull/87) targeting `dev`, complete the full required workflow, then merge and publish `26.10.25-alpha2`. The maintainer authorized the merge and alpha2 publication after validation. Do not promote to `test` or `main`. Deployment to Catbug is a separate gate and is not authorized by this request.

## Starting state

- Local and remote cycle branch: `26.10.25`; starting HEAD: `9eb9dc4d4ded09c9346a8146a919f6cf3c31dc20`.
- PR #87 is open; auto-merge is disabled. The PR currently fixes #85 and references #83 and #86.
- Latest published candidate: `26.10.25-alpha1`.
- The worktree was clean apart from a local untracked `.serena/` directory generated while attempting Serena setup. Do not include it in the handoff commit.
- The current branch is explicitly documented in `docs/DEVELOPMENT.md`. Some ignored `.private/AGENTS.md` text still names `26.10.18`; that note is stale for this cycle.
- No implementation for #88 has been made, and no live database writes were performed for it.

## Issue investigation

The duplicate entries are backed by separate `tv_shows` parents: a TMDb-backed parent and a negative-ID local placeholder for the apparent same series. Catalog grouping is by `show_id`; the audience classification difference follows from differing parent metadata, not from audience being used as a grouping key.

Read-only Catbug inspection found 15 such pairs. Examples:

- Pokémon Horizons: show 78 / TMDb 220150, TV-Y7, 45 episodes; placeholder 193 / TMDb -15, no rating, 55 episodes.
- Phineas and Ferb (2007): show 126 / TMDb 1877, TV-G, 262 episodes; placeholder 191 / TMDb -13, no rating, 2 episodes.
- DAN DA DAN (2024): show 46 / TMDb 240411, TV-MA, 12 episodes; placeholder 188 / TMDb -10, no rating, 12 episodes.
- Other pairs include Frieren, Kaiju No 8, Mashle, Re:ZERO, Shangri-La Frontier, Solo Leveling, Apothecary Diaries, Dragons, Powerpuff Girls, Animaniacs, Rugrats and Reincarnated Aristocrat.

Treat those observations as evidence of a parent-record split, not proof that equal display names are safe to merge.

## Implementation constraints

Build conservative identity reconciliation and prevent the split from recurring. Do not hide duplicates only in the UI or merge on display title alone. Remakes, regions, provider IDs and conflicting manual metadata must remain distinguishable.

Preserve media IDs, episode associations, manual corrections and currently playing programs. In particular, reconciliation must not make known mature content eligible for Kids. The owner has made fresh manual edits since an earlier audience reset: **do not clear audience overrides again**.

Review the existing flows in:

- `server/internal/library/catalog.go`: grouping and catalog updates; show edits can create negative-ID placeholders for episodes without a parent.
- `server/internal/library/identify_storage.go`: automatic parent reattachment.
- `server/internal/library/enrich.go`: `resolveShow` and `enrichEpisode`.
- `server/internal/library/lookupname.go`: year, region and provider-ID qualifiers.
- `server/internal/library/identify.go`: series/folder identification.
- `server/internal/refresh/refresh.go` and `server/cmd/server/main.go`: refresh and startup integration.

Add isolated regression tests covering existing duplicate repair, repeated scans, missing ratings, ambiguous identities and manual conflicts. Tests must not mutate Catbug or other live storage.

## Validation and PR history

- Quality and CodeQL passed on `9eb9dc4`.
- The first full run exposed a Git-free CI version-reporting problem, fixed on `9eb9dc4`; an isolated Git-free CI build verified the bundled version.
- One full run timed out in `tests/e2e/guide-live.cjs:193`, reached from line 256 during fullscreen channel navigation. That unchanged scenario then passed five consecutive times in the CI image with two CPUs.
- The second run was cancelled when #88 was added; it is neither a passing nor a failing result.
- A separate stale catalog assertion was corrected in `c90bcbc`; its focused suite passed.
- Run the full required PR workflow again after the #88 implementation is committed. Do not skip or weaken tests. Investigate if the guide timeout recurs.

The PR description must be updated with #88, the final behavior, risks and actual validation results. Its existing validation notes describe prior work and must not be represented as full validation of the final combined PR.

## Merge and publication checklist

After implementation, update PR #87 to include the #88 fix and run the full required checks. Merge only after all required checks pass. The merge to `dev` triggers image and signed Android candidate publication; verify it produces alpha2 rather than dispatching a duplicate candidate.

Before considering delivery complete, verify:

- Final source commit and release tag provenance.
- Published image digest and attestations.
- Signed phone/tablet and TV APKs, checksums and SBOMs.
- GitHub Release title exactly `26.10.25-alpha2` and description sections in order: `# Download Now` (phone/tablet and TV APK links), `# New Features`, `# Bugfixes`, `# Known Issues`. Include explicit empty-section messages and linked issue titles for fixes and prioritized remaining bugs. Generate from GitHub PRs/issues since the predecessor tag; save the description in GitHub Releases, not a repository fragment.
- Issue status: code merged to `dev` is **In Review**, not Done. GitHub may auto-close #85/#88 because `dev` is the default branch; reopen them and set In Review until delivery to `test`/`main`. Keep #83 open for its broader unfinished scope.

Catbug currently runs the already tested #86 image at commit `2817afd44cd9aea27cd7c2381d5727d822da55a6`. Preserve its single Compose service, configuration, credentials, media and database. Do not deploy or promote to `test`/`main` as part of this handoff.
