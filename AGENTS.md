# Instructions for coding assistants

Read [the project brief](docs/PROJECT_BRIEF.md), [contributing](CONTRIBUTING.md) and [delivery](docs/CI_CD.md) before changing product or release behavior.

## Work safely

- Use only the primary repository checkout; do not create or maintain parallel worktrees. Use the active dated branch in [development setup](docs/DEVELOPMENT.md). `build` and its legacy `work.py` workflow are retired for this cycle.
- Follow the maintainer's current checkout and branch instructions. Preserve unrelated changes and coordinate shared Git operations.
- Commit completed work with clear messages. Push only when authorized by the active workflow.
- Update the development preview when requested. Run relevant checks; full release suites require the maintainer's release approval.
- Never merge, deploy, publish an image, promote a branch or change repository visibility without authorization. Follow each approved gate in order.
- Keep issues current in the maintainer-designated development tracker (see local notes). This repository’s issues are reserved for tester/user feedback. A planning-only request authorizes a ticket, not implementation.

## Preserve boundaries

Keep live databases, credentials, media and configuration intact. Tests use temporary storage and synthetic fixtures. Never reset a live account or database to make a test pass. Owner-provided API access does not authorize destructive actions or deployments.

Document public contracts and explain non-obvious logic with useful comments. Do not add environment variables without maintainer review. Never commit secrets, workstation details or build transcripts. Report actual checks and their results, including limitations.

## Local maintainer notes

If present, read ignored `.private/AGENTS.md` for checkout ownership, local credentials and approved release procedures. These local notes supplement the public instructions; direct maintainer instructions take precedence. `.private/` is excluded from Git and container build contexts.

## Review and delivery

Use the repository PR template. Preserve the exact tested image during promotion; do not rebuild it under a release tag. Keep releases traceable to a commit and digest. Remove obsolete branches only after verifying their work is merged, they are unused, and deletion is authorized.

## Release descriptions

Follow [release notes](docs/RELEASE_NOTES.md): titles are only `YYYY-MM-DD-alphaN`, `YYYY-MM-DD-rcN` or `YYYY-MM-DD-stable`. Render `# New Features`, `# Bugfixes` and `# Known Issues`, including empty-section messages. Link issue titles for fixes and prioritized remaining bugs; maintain `docs/release-known-issues.md`. Compare product changes against the predecessor tag. Omit introductions and visible build details; preserve hidden provenance and exact artifacts. Display titles never rename tags or installed APK versions.
