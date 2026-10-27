# Release descriptions

Release descriptions live in GitHub Releases; PRs and issues supply their content. Do not write `docs/release-notes/` fragments or a repository known-issue list. Implementation notes belong in issues/PRs; review captures stay outside Git.

Titles are only `YY.MM.DD-nightly`, `YY.MM.DD-alphaN`, `YY.MM.DD-rcN` or `YY.MM.DD-stable`. Preserve tag casing, installed APK versions, exact signed artifacts and hidden provenance.

Every published description starts with `# Download Now`, linking that release’s phone/tablet and TV APKs, followed by `# New Features`, `# Bugfixes` and `# Known Issues`, in that order. Empty sections use “No new features in this release.”, “No bugfixes in this release.” and “No known issues are currently listed for this release.” Bugfixes and known issues link exact GitHub issue titles; a fix may append a brief explanation after the link.

## Generate a candidate

The helper compares the predecessor release tag with the exact candidate SHA using Git ancestry. It reads PRs associated with commits in that range and includes a PR only when its merged commit is in the range. First releases use reachable history; merge dates do not determine inclusion.

Maintain product metadata in GitHub:

- Label feature PRs/issues `feature` or `enhancement`; conventional `feat:`/`Feature:` titles also identify features. A feature PR without a linked feature issue uses its title and PR link.
- Link resolved bugs using GitHub closing issue references. Bugfix entries use the linked bug issue’s exact title and URL, even before branch promotion. Bug-labelled PRs without a closing bug issue fail generation rather than producing unlinked fixes.
- Mark internal PRs with `documentation`, `dependencies`, `chore`, `ci` or `release:skip` to omit them. Unclassified PRs without product issue references do not become feature claims.
- Known Issues uses currently open bugs, excluding bugs resolved by included PRs and issues marked duplicate, invalid, wontfix or release:skip. P0/critical, P1/high and P2/medium priority labels sort first; otherwise issue number determines order. Feature requests are excluded.

Metadata reads paginate and API failures stop generation. Review the resulting descriptions during normal release acceptance; no handwritten draft or repository fragment is required. GitHub state is captured when the candidate is built. Built-candidate reruns reuse their description after checking source/predecessor provenance, so later issue edits do not rewrite that snapshot.

Publication follows maintainer-approved [delivery gates](CI_CD.md).

RC/Stable promotion reads the tested candidate’s published description and preserves its known issues. Promotions rebuild signed APKs with the target-stage version and a higher version code; Docker images retain the tested digest. Same-source promotions add no new product changes. Description-only edits never regenerate APKs.
