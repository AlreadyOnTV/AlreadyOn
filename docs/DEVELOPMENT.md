# Development setup

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

Install the versions documented in [toolchain](TOOLCHAIN.md), Git, FFmpeg and FFprobe. Use rootless Podman or Docker for production-image validation. `make doctor` checks prerequisites; inspect its output before installing or changing host packages.

Local Podman builds pull the published GHCR runtime/CI bases, verify recipe and parent compatibility, and pin immutable image IDs before compiling the checkout. Missing or incompatible bases use compatible local caches or local builds; authentication/network failures stop the build. Published bases follow CI recipe policy; local fallbacks expire after seven days. `make refresh-local-bases` explicitly rebuilds both bases. Nothing is published automatically.

## Native preview

Install web dependencies with `cd web && npm ci`, then return to the repository root and run `make preview`. Open <https://localhost:5173>. Backend changes require restarting the preview; Vite reloads browser changes.

Use `make preview-status` and `make preview-stop` to inspect or stop this checkout's services. The default backend port is 15900. Existing preview port overrides are supported by `scripts/preview.sh`; coordinate ports when running multiple instances.

## Development and release work

Use the primary checkout and the long-lived `dev` and `main` branches only. Commit active development directly to `dev`; validate with the HTTPS preview at <https://localhost:5173>, disposable Docker tests, and USB-connected phone/TV builds as needed. When ready, open a PR from `dev` to `main`. CodeQL, dependency review, Android and full e2e/runtime validation must pass before auto-merge. Maintainers manually publish Nightly from `dev` or `main`; a `dev` Nightly becomes eligible for Stable after its source reaches `main`. Run **Build and Publish** with `operation: stable` on `main` to add Docker `latest` and build fresh Stable APKs. Follow [release-note conventions](RELEASE_NOTES.md).

Trust this instance's local certificate only on your development machine. Do not weaken remote TLS validation. Local data lives under ignored `server/data/`; keep it across restarts.

## Accounts and private configuration

On a fresh checkout, create an owner through first-run setup. Optional local provisioning reads ignored `.private/preview-login.json` with `username` and `password` fields. Restrict that file to your user. `make preview-login` explicitly restores the configured local account password; it preserves other data. It is never a production credential tool.

Store machine-specific notes in ignored `.private/`. Do not commit home paths, host aliases, tokens or signing material. Native clients have separate [Android setup](ANDROID.md).

## Validate

Run `make lint` and relevant Go tests. Use isolated [browser tests](../tests/e2e/README.md) and [runtime tests](../tests/integration/README.md). [Delivery](CI_CD.md) describes release checks. Never point destructive tests at your preview or a live server.

Use the [documentation index](README.md) for subsystem contracts, platform guidance and release procedures.
