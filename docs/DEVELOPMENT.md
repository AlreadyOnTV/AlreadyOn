# Development setup

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

Install the versions documented in [toolchain](TOOLCHAIN.md), Git, FFmpeg and FFprobe. Use rootless Podman or Docker for production-image validation. `make doctor` checks prerequisites; inspect its output before installing or changing host packages.

## Native preview

Install web dependencies with `cd web && npm ci`, then return to the repository root and run `make preview`. Open <https://localhost:5173>. Backend changes require restarting the preview; Vite reloads browser changes.

Use `make preview-status` and `make preview-stop` to inspect or stop this checkout's services. The default backend port is 15900. Existing preview port overrides are supported by `scripts/preview.sh`; coordinate ports when running multiple instances.

## Single-checkout release work

Use the primary repository checkout; do not create parallel worktrees. Begin with `python3 scripts/work.py start build`, which refreshes the local release branch from current `dev` only when its existing commits are already merged. Test locally at <https://localhost:5173>. When ready, run `python3 scripts/work.py publish [YYYY-MM-DD]` to push the date branch/tag and open the dev PR. Do not start another release date while one lacks Stable.

Trust this instance's local certificate only on your development machine. Do not weaken remote TLS validation. Local data lives under ignored `server/data/`; keep it across restarts.

## Accounts and private configuration

On a fresh checkout, create an owner through first-run setup. Optional local provisioning reads ignored `.private/preview-login.json` with `username` and `password` fields. Restrict that file to your user. `make preview-login` explicitly restores the configured local account password; it preserves other data. It is never a production credential tool.

Store machine-specific notes in ignored `.private/`. Do not commit home paths, host aliases, tokens or signing material. Native clients have separate [Android setup](ANDROID.md).

## Validate

Run `make lint` and relevant Go tests. Use isolated [browser tests](../tests/e2e/README.md) and [runtime tests](../tests/integration/README.md) when requested. [Delivery](CI_CD.md) describes release checks. Never point destructive tests at your preview or a live server.

The guide profile menu lists Dashboard, Profile Settings, Switch Profile, then Sign Out on every device. WebUI inserts Admin Settings for profile admins and Server Settings for server owners before Sign Out. Dashboard is a read-only page for every signed-in profile; it is no longer a Server Settings tab.
