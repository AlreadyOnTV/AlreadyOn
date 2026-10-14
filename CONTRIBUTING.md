# Contributing

[![AlreadyOn](web/public/branding/header-doc.svg)](README.md)

Start with [the project brief](docs/PROJECT_BRIEF.md) and [development setup](docs/DEVELOPMENT.md). Use this repository’s issues for tester/user feedback; maintainers track development work separately. Coordinate substantial implementation changes with maintainers before starting. Assistants must also read [AGENTS.md](AGENTS.md).

## Make a change

1. External contributors normally use a fork and a focused branch. Maintainers use one checkout and the active cycle branch documented in [development setup](docs/DEVELOPMENT.md). The current branch is `26.10.18`; `build` has been retired for this cycle.
2. Implement one coherent change. Preserve existing behavior outside its scope.
3. Update the relevant guide and [OpenAPI](api/openapi.yaml) when contracts change. Explain unusual logic in source comments. Include a [release-note entry](docs/RELEASE_NOTES.md) for user-facing changes. Use `# New Features` or `# Bugfixes`; fixes link exact issue titles. Update [known issues](docs/release-known-issues.md) when prioritized bugs are added or fixed. Omit internal maintenance.
4. Run relevant checks and describe results in the PR. Include screenshots for visible interface changes and reproduction steps for fixes.
5. For maintainer releases, test against the native preview, then open the active dated branch’s PR to dev when validation is approved. The PR must pass code scanning and full e2e before merge; use the documented exact-artifact promotion commands afterward.

Use `make lint` and `python3 scripts/ci/check-documentation.py` for static checks. Go tests run through `make test`; browser and packaged-runtime tests are described in [delivery](docs/CI_CD.md). Never use live media or a production database as destructive fixtures.

Do not commit secrets, local configuration, signing keys, generated media or test reports. Keep private notes in ignored `.private/`. Ignoring a previously tracked file does not erase its history.

## Review

Use the [PR template](.github/PULL_REQUEST_TEMPLATE.md). Explain the problem, resulting behavior, validation and material risks. Prefer clear boundaries and straightforward code over speculative abstractions. Maintainers decide merge, deployment and release timing.

Report vulnerabilities privately through [SECURITY.md](SECURITY.md). AlreadyOn uses [GNU GPLv3](LICENSE); licensing and third-party redistribution obligations apply to every contribution.
