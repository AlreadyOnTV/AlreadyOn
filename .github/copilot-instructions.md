# Repository instructions

Follow [AGENTS.md](../AGENTS.md), including ignored local maintainer notes when present. Read the linked project, contribution and delivery guides before changing behavior.

Preserve unrelated work and coordinate shared checkouts. Keep changes small and documented; run appropriate checks. Never skip maintainer-controlled release, deployment or publication gates. Planning tickets do not authorize implementation.

Develop directly on `dev`; only `dev` and `main` are long-lived branches. PRs from dev to main require CodeQL, Dependabot dependency review and full e2e/runtime checks, then auto-merge. Main merges publish the current-date Nightly image and signed phone/TV APKs. The manual Stable workflow retags the newest Nightly digest as `latest` and builds fresh Stable APKs from its exact source. Date collisions may be replaced only by those validated workflows, with higher APK version codes and updated provenance; never edit release assets or refs manually. Follow [release notes](../docs/RELEASE_NOTES.md): `YY.MM.DD-nightly`/`YY.MM.DD-stable`, Download Now, New Features, Bugfixes, Known Issues and explicit empty sections. Do not commit release-note fragments or review snapshots.
