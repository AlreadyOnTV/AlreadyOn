# Contributing

[![AlreadyOn](web/public/branding/header-doc.svg)](README.md)

Use the [documentation index](docs/README.md) for current guides. Start with [the project brief](docs/PROJECT_BRIEF.md) and [development setup](docs/DEVELOPMENT.md). Use this repository’s issues for tester/user feedback; maintainers track development work separately. Coordinate substantial implementation changes with maintainers before starting. Assistants must also read [AGENTS.md](AGENTS.md).

## Make a change

1. External contributors normally use a fork and focused branch. Maintainers use one checkout and develop directly on `dev`, as documented in [development setup](docs/DEVELOPMENT.md).
2. Implement one coherent change. Preserve existing behavior outside its scope.
3. Update the relevant guide and [OpenAPI](api/openapi.yaml) when contracts change. Explain unusual logic in source comments. Describe user-facing changes in the PR for [release preparation](docs/RELEASE_NOTES.md). Release descriptions are generated from product-labelled PRs and linked issues, not repository Markdown. Fixes link exact issue titles. Omit internal maintenance.
4. Run relevant checks and describe results in the PR. Include screenshots for visible interface changes and reproduction steps for fixes.
5. For maintainer releases, validate against the native preview and test devices, then open a PR from `dev` to `main`. CodeQL, Dependabot dependency review, Android checks and full e2e/runtime checks must pass before auto-merge. Maintainers manually publish Nightly from `dev` or `main`; Stable is manually published from the latest validated Nightly reachable from `main`.

Use `make lint` and `python3 scripts/ci/check-documentation.py` for static checks. Go tests run through `make test`; browser and packaged-runtime tests are described in [delivery](docs/CI_CD.md). Never use live media or a production database as destructive fixtures.

Do not commit secrets, local configuration, signing keys, generated media or test reports. Keep private notes in ignored `.private/`. Ignoring a previously tracked file does not erase its history.

## Review

Use the [PR template](.github/PULL_REQUEST_TEMPLATE.md). Explain the problem, resulting behavior, validation and material risks. Prefer clear boundaries and straightforward code over speculative abstractions. Maintainers decide merge, deployment and release timing.

Report vulnerabilities privately through [SECURITY.md](SECURITY.md). AlreadyOn uses [GNU GPLv3](LICENSE); licensing and third-party redistribution obligations apply to every contribution.

Keep documentation focused on current setup, public contracts, operating procedures and non-obvious decisions. Contributor guides have a 500-word limit; the standard Code of Conduct is exempt, but its links are still checked. Record completed implementation checklists, one-off measurements and validation transcripts in their issue or PR. Keep release descriptions on GitHub and review snapshots outside Git. Prefer updating an existing guide over creating a feature-by-feature work log.
