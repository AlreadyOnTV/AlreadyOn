# Synthetic fixtures

[![AlreadyOn](../../web/public/branding/header-doc.svg)](../../README.md)

Fixtures are generated media for isolated tests. Run the harness or `scripts/gen-fixtures.sh` rather than committing generated binaries.

Generated output is ignored. Do not replace fixtures with personal media or point cleanup at a live library. Keep cases small, deterministic and sufficient to reproduce the behavior under test.

Demos also use disposable storage and synthetic accounts/media. Never publish owner credentials, management keys, real library metadata or private filesystem paths. Use optional ignored local provisioning only as described in [development setup](../../docs/DEVELOPMENT.md); do not reset an existing account/database for a demonstration.
