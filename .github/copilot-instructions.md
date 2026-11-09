# Repository instructions

Follow [AGENTS.md](../AGENTS.md), including ignored local maintainer notes when present. Read the linked project, contribution and delivery guides before changing behavior.

Preserve unrelated work and coordinate shared checkouts. Keep changes small and documented; run appropriate checks. Never skip maintainer-controlled release, deployment or publication gates. Planning tickets do not authorize implementation.

Develop directly on `dev`; only `dev` and `main` are long-lived branches. PRs to `dev` and `main` require CodeQL, Dependabot dependency review, Android checks and full e2e/runtime checks, then auto-merge. Nightly and Stable publication are manual; Nightly may target `dev` or `main`, and Stable reuses a validated Nightly reachable from `main`. Date collisions may be replaced only by those validated workflows, with higher APK version codes and updated provenance; never edit release assets or refs manually. Follow [release notes](../docs/RELEASE_NOTES.md): `YY.MM.DD-nightly`/`YY.MM.DD-stable`, Download Now, New Features, Bugfixes, Known Issues and explicit empty sections. Do not commit release-note fragments or review snapshots.
