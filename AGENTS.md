# Instructions for coding assistants

Read [the project brief](docs/PROJECT_BRIEF.md), [contributing](CONTRIBUTING.md) and [delivery](docs/CI_CD.md) before changing product or release behavior.

## Work safely

- Use the primary checkout; do not create or maintain parallel worktrees. Develop directly on `dev`; the only long-lived branches are `dev` and `main`.
- Follow the maintainer's current checkout and branch instructions. Preserve unrelated changes; coordinate Git operations.
- Commit completed work with clear messages and push to `dev` when authorized. Release PRs target `main`.
- Update the development preview when requested. Run relevant checks; full release suites require the maintainer's release approval.
- Never merge, deploy, publish an image, promote a branch or change repository visibility without authorization. Follow each approved gate in order.
- Keep issues current in the maintainer-designated development tracker (see local notes). This repository’s issues are reserved for tester/user feedback. A planning-only request authorizes a ticket, not implementation.

## Preserve boundaries

Keep live databases, credentials, media and configuration intact. Tests use temporary storage and synthetic fixtures. Never reset a live account or database to make a test pass. Owner-provided API access does not authorize destructive actions or deployments.

Document public contracts and explain non-obvious logic with useful comments. Do not add environment variables without maintainer review. Never commit secrets, workstation details, build transcripts or review snapshots. Report actual checks and their results, including limitations.

## Local maintainer notes

If present, read ignored `.private/AGENTS.md` for checkout ownership, local credentials and approved release procedures. Local notes supplement public instructions; direct maintainer instructions take precedence. `.private/` is excluded from Git and container build contexts.

## Review and delivery

Use the repository PR template. Main PRs require CodeQL, Dependabot dependency review and full e2e/runtime validation; successful PRs auto-merge. Keep releases traceable to source, digest and signed artifacts. Validated release workflows may replace current-date Nightly/Stable assets with higher APK version codes. Never rewrite refs or assets manually. Keep only `dev` and `main` after delivery.

## Release descriptions

Follow [release naming](docs/RELEASE_NAMING.md) and [release notes](docs/RELEASE_NOTES.md): titles are `YY.MM.DD-nightly` or `YY.MM.DD-stable`. Render `# Download Now` with phone/tablet and TV APK links, then `# New Features`, `# Bugfixes` and `# Known Issues`, including empty-section messages. Link issue titles for fixes and prioritized remaining bugs; generate descriptions from GitHub PRs/issues; never write repository fragments. Compare changes against the predecessor release and preserve provenance when validated workflows replace current-date assets.
