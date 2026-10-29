# Development setup

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

Install the versions documented in [toolchain](TOOLCHAIN.md), Git, FFmpeg and FFprobe. Use rootless Podman or Docker for production-image validation. `make doctor` checks prerequisites; inspect its output before installing or changing host packages.

## Native preview

Install web dependencies with `cd web && npm ci`, then return to the repository root and run `make preview`. Open <https://localhost:5173>. Backend changes require restarting the preview; Vite reloads browser changes.

Use `make preview-status` and `make preview-stop` to inspect or stop this checkout's services. The default backend port is 15900. Existing preview port overrides are supported by `scripts/preview.sh`; coordinate ports when running multiple instances.

## Single-checkout release work

Use the primary repository checkout; do not create parallel worktrees. The active maintainer cycle uses local and remote branch `26.10.29`, based on `dev`; commit and push cycle work there. Test locally at <https://localhost:5173>. When ready for approved validation, open a PR from `26.10.29` to `dev`. After merging validated work, dev publishes a nightly; RC/Stable await maintainer acceptance. Retain the dated branch until its work is delivered; see [cycle instructions](ORGANIZATION.md). Follow [release-note conventions](RELEASE_NOTES.md) for titles, changes and known issues.

Trust this instance's local certificate only on your development machine. Do not weaken remote TLS validation. Local data lives under ignored `server/data/`; keep it across restarts.

## Accounts and private configuration

On a fresh checkout, create an owner through first-run setup. Optional local provisioning reads ignored `.private/preview-login.json` with `username` and `password` fields. Restrict that file to your user. `make preview-login` explicitly restores the configured local account password; it preserves other data. It is never a production credential tool.

Store machine-specific notes in ignored `.private/`. Do not commit home paths, host aliases, tokens or signing material. Native clients have separate [Android setup](ANDROID.md).

## Validate

Run `make lint` and relevant Go tests. Use isolated [browser tests](../tests/e2e/README.md) and [runtime tests](../tests/integration/README.md) when requested. [Delivery](CI_CD.md) describes release checks. Never point destructive tests at your preview or a live server.

Use the [documentation index](README.md) for subsystem contracts, platform guidance and release procedures.
