# AlreadyOn

**Your library, tuned to you**

[![AlreadyOn](web/public/branding/banner.svg)](README.md)

[![Install](web/public/branding/install-badge.svg)](deploy/README.md) [![Develop](web/public/branding/develop-badge.svg)](docs/DEVELOPMENT.md) [![Contribute](web/public/branding/contribute-badge.svg)](CONTRIBUTING.md) [![GPL-3.0](web/public/branding/license-badge.svg)](LICENSE)

AlreadyOn turns your own media library into scheduled television channels. Browse a guide, tune into an airing, and let playback continue across scheduled programs.

The server uses Go and SQLite; the browser client uses React and TypeScript. Android phone/tablet and Google TV clients are available; Apple clients are not planned. Media stays on your server. Metadata providers are optional.

## Try it

Use [the container guide](deploy/README.md) for installation. Configure media folders, create the owner account through first-run setup, and scan the library. Mount media read-only unless you explicitly need file-management features.

For source development, follow [development setup](docs/DEVELOPMENT.md). Run `make preview` and open <https://localhost:5173>. Trust only the certificate for your own local instance. No shared account or remote server is supplied.

## Contribute

Read [CONTRIBUTING.md](CONTRIBUTING.md), [the project brief](docs/PROJECT_BRIEF.md) and [delivery guidance](docs/CI_CD.md). API contracts live in [OpenAPI](api/openapi.yaml). [Playback](docs/PLAYBACK.md), [scheduling](docs/SCHEDULING.md) and [security reporting](SECURITY.md) explain the main boundaries.

The [code-quality roadmap](docs/CODE_QUALITY.md) tracks public-release review work.

Changes should be small, documented and verified with relevant checks. Use isolated synthetic fixtures for destructive or full end-to-end tests. Release and deployment decisions belong to the maintainer.

AlreadyOn is under active development. Browser, codec and GPU support varies; validate the devices you intend to use. See [the license](LICENSE) before redistributing builds.
