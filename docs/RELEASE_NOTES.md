# Release descriptions

Release descriptions live in GitHub Releases; PRs, issues and commit subjects supply their content. Do not write `docs/release-notes/` fragments or a repository known-issue list. Keep review captures outside Git.

Titles are only `YY.MM.DD-nightly` or `YY.MM.DD-stable`. Preserve installed APK versions, exact signed artifacts and hidden provenance. Historical alpha/RC releases remain unchanged.

Every published description starts with `# Download Now`, linking that release’s phone/tablet and TV APKs, followed by `# New Features`, `# Bugfixes`, `# Other Changes` and `# Known Issues`, in that order. Each entry is one simple, single-line bulleted summary with its source link. Empty sections use bulleted “No new features in this release.”, “No bugfixes in this release.” “No other changes in this release.” and “No known issues are currently listed for this release.” Bugfixes and known issues link exact GitHub issue titles; a fix may append a brief explanation after the link.

## Generate a candidate

The helper compares the predecessor tag with the candidate SHA using Git ancestry. Nightly compares with the previous Nightly or Stable, whichever came last; Stable compares with the previous Stable, falling back to the prior dated release when a channel has no predecessor. It reads PRs associated with commits in that range and includes a PR only when its merged commit is in the range. First releases use reachable history; merge dates do not determine inclusion.

Maintain product metadata in GitHub:

- Label feature PRs/issues `feature` or `enhancement`; conventional `feat:`/`Feature:` titles also identify features. A feature PR without a linked feature issue uses its title and PR link.
- Link resolved bugs using GitHub closing issue references. Bugfix entries use the linked bug issue’s exact title and URL, even before branch promotion. Bug-labelled PRs without a closing bug issue fail generation rather than producing unlinked fixes.
- Unclassified, documentation, dependency and CI PRs appear under Other Changes using their titles and links. Direct commits without a merged PR use their subjects and commit links; integration merges are not duplicated. Only `release:skip` explicitly omits a PR. Source-identical releases receive a no-source-changes bullet.
- Known Issues uses currently open bugs, excluding bugs resolved by included PRs and issues marked duplicate, invalid, wontfix or release:skip. P0/critical, P1/high and P2/medium priority labels sort first; otherwise issue number determines order. Feature requests are excluded.

API reads paginate; failures stop generation. Review descriptions during release acceptance; no handwritten draft or repository fragment is required. GitHub state is captured when the candidate is built. Exact-source reruns reuse their description after checking provenance, so later issue edits do not rewrite that snapshot. A validated same-date Nightly replacement with a new source SHA regenerates notes from that source and its baseline.

Publication follows maintainer-approved [delivery gates](CI_CD.md).

Stable consolidates its predecessor range and preserves the selected Nightly’s known issues. It rebuilds signed APKs with the Stable version and a higher code while Docker `latest` reuses the Nightly digest. Description-only backfills replace changelog content and preserve downloads, signed assets, tags and hidden provenance. Historical alpha/RC descriptions stay unchanged.
