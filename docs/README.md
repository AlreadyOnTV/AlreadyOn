# Documentation

Start with [the project brief](PROJECT_BRIEF.md), [contributing](../CONTRIBUTING.md) and [development setup](DEVELOPMENT.md). Coding assistants also follow [AGENTS.md](../AGENTS.md); Claude and Copilot entry points delegate to those shared instructions.

| Work | References |
| --- | --- |
| Build and test | [Toolchain](TOOLCHAIN.md), [Android builds](../apps/android/README.md), [emulators](ANDROID_EMULATOR.md), [browser tests](../tests/e2e/README.md), [runtime tests](../tests/integration/README.md), [fixtures](../tests/fixtures/README.md) |
| Server and API | [Server](../server/README.md), [OpenAPI](../api/openapi.yaml), [compatibility](../api/COMPATIBILITY.md), [scheduling and filters](SCHEDULING.md) |
| Playback | [Delivery and sessions](PLAYBACK.md), [channel navigation](CHANNEL_NAVIGATION.md), [subtitles](SUBTITLES.md) |
| Profiles and WebUI | [Profile menu and sessions](PROFILE_MENU.md), [profile creation](PROFILE_CREATION.md), [credential entry](SAVED_CREDENTIALS.md), [web client](../web/README.md) |
| Native clients | [Android](ANDROID.md), [Google TV](GOOGLE_TV.md), [manual connection](SERVER_CONNECTION.md), [TV remote](TV_REMOTE_NAVIGATION.md), [TV profile settings](TV_PROFILE_SETTINGS.md), [physical review](TV_DEVICE_REVIEW.md), [updates](ANDROID_UPDATES.md) |
| Security and installation | [Security/reporting](../SECURITY.md), [boundaries](SECURITY_BOUNDARIES.md), [TLS](TLS.md), [deployment](../deploy/README.md) |
| Release and maintenance | [Delivery gates](CI_CD.md), [APK releases](ANDROID_RELEASES.md), [signing](ANDROID_SIGNING.md), [Stable readiness](STABLE_READINESS.md), [provenance](ARTIFACT_PROVENANCE.md), [retention](IMAGE_RETENTION.md), [dependencies](DEPENDENCY_MAINTENANCE.md), [scanning](VULNERABILITY_SCANNING.md) |
| Identity and presentation | [Organization/cycle](ORGANIZATION.md), [branding](BRANDING.md), [website](WEBSITE.md), [screenshot provenance](../website/assets/screenshots/README.md) |

[Release naming](RELEASE_NAMING.md), [release notes](RELEASE_NOTES.md), `release-notes/` and [known issues](release-known-issues.md) are inputs to release preparation. Historical fragments intentionally describe their original changes; read the current guides for present behavior. Keep fragments intact because generation compares source snapshots and predecessor tags.

Keep durable contracts and repeatable procedures here. Completed acceptance reports, implementation diaries, benchmark snapshots and review captures belong in their issue/PR or Git history, rather than a second maintained specification.
