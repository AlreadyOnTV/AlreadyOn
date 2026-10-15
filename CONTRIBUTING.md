# Contributing

[![AlreadyOn](web/public/branding/header-doc.svg)](README.md)

Use the [documentation index](docs/README.md) for current guides. Start with [the project brief](docs/PROJECT_BRIEF.md) and [development setup](docs/DEVELOPMENT.md). Use this repository’s issues for tester/user feedback; maintainers track development work separately. Coordinate substantial implementation changes with maintainers before starting. Assistants must also read [AGENTS.md](AGENTS.md).

## Make a change

1. External contributors normally use a fork and a focused branch. Maintainers use one checkout and the active cycle branch documented in [development setup](docs/DEVELOPMENT.md). The current branch is `26.10.18`; `build` has been retired for this cycle.
2. Implement one coherent change. Preserve existing behavior outside its scope.
3. Update the relevant guide and [OpenAPI](api/openapi.yaml) when contracts change. Explain unusual logic in source comments. Describe user-facing changes in the PR for [release preparation](docs/RELEASE_NOTES.md). Reviewed descriptions and known issues live in GitHub Release drafts, not repository Markdown. Fixes link exact issue titles. Omit internal maintenance.
4. Run relevant checks and describe results in the PR. Include screenshots for visible interface changes and reproduction steps for fixes.
5. For maintainer releases, test against the native preview, then open the active dated branch’s PR to dev when validation is approved. The PR must pass code scanning and full e2e before merge; use the documented exact-artifact promotion commands afterward.

Use `make lint` and `python3 scripts/ci/check-documentation.py` for static checks. Go tests run through `make test`; browser and packaged-runtime tests are described in [delivery](docs/CI_CD.md). Never use live media or a production database as destructive fixtures.

Do not commit secrets, local configuration, signing keys, generated media or test reports. Keep private notes in ignored `.private/`. Ignoring a previously tracked file does not erase its history.

## Review

Use the [PR template](.github/PULL_REQUEST_TEMPLATE.md). Explain the problem, resulting behavior, validation and material risks. Prefer clear boundaries and straightforward code over speculative abstractions. Maintainers decide merge, deployment and release timing.

Report vulnerabilities privately through [SECURITY.md](SECURITY.md). AlreadyOn uses [GNU GPLv3](LICENSE); licensing and third-party redistribution obligations apply to every contribution.

Keep documentation focused on current setup, public contracts, operating procedures and non-obvious decisions. Record completed implementation checklists, one-off measurements and validation transcripts in their issue or PR. Keep release descriptions on GitHub and review snapshots outside Git. Prefer updating an existing guide over creating a feature-by-feature work log.
