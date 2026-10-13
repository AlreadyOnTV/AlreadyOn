SHELL := /bin/bash

.PHONY: check-ubuntu-lts doctor dev test test-integration test-e2e lint check-web-storage build image test-container container-build check-toolchain check-public-docs

doctor:
	@scripts/doctor.sh

dev:
	@scripts/preview.sh start

test:
	@cd server && go test ./...
	@echo "web: browser coverage runs separately with make test-e2e"

test-integration: test-container

test-e2e:
	@timeout 3600 tests/e2e/playback.sh

lint: check-web-storage check-toolchain check-public-docs
	@cd server && unformatted=$$(gofmt -l .) && \
	  { test -z "$$unformatted" || { printf 'Run gofmt on:\n%s\n' "$$unformatted"; exit 1; }; } && go vet ./...
	@cd web && npm run lint

check-public-docs:
	@python3 scripts/ci/check-public-docs.py

check-web-storage:
	@scripts/check-web-storage.sh

build:
	@cd server && go build -ldflags "-X github.com/alreadyonapp/alreadyon/server/internal/httpapi.nativeBuildVersion=$$(TZ=America/New_York date +%y.%m.%d)_$$(git rev-parse --short=7 HEAD)" -o alreadyon-server ./cmd/server
	@cd web && npm run build

check-toolchain:
	@python3 scripts/check-toolchain.py

# Needs network and a container engine; fails when ubuntu:latest has moved to a newer LTS.
check-ubuntu-lts:
	@python3 scripts/check-toolchain.py --check-latest-lts

image: container-build

# Approved release checks use synthetic data and disposable packaged-runtime storage.
.PHONY: release-check test-go-runtime local-bases refresh-local-bases
release-check:
	@test -z "$$(git status --porcelain)" || { echo 'Commit and push build changes before release checks'; exit 1; }
	@$(MAKE) --no-print-directory lint
	@python3 scripts/ci/check-documentation.py
	@python3 scripts/ci/check-syntax.py
	@go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
	@python3 -m unittest discover -s scripts/ci -p 'test_*.py'
	@$(MAKE) --no-print-directory image
	@$(MAKE) --no-print-directory test-go-runtime
	@$(MAKE) --no-print-directory test-e2e
	@CONTAINER_ENGINE=podman $(MAKE) --no-print-directory test-container

test-go-runtime:
	@scripts/test-go-runtime.sh

local-bases:
	@python3 scripts/local-bases.py

refresh-local-bases:
	@python3 scripts/local-bases.py --refresh

container-build:
	@scripts/container.sh build

test-container:
	@scripts/container.sh test

# Checkout preview services outlive agent turns and retain the existing local DB.
.PHONY: preview preview-status preview-stop preview-build preview-login
preview-build:
	@cd server && go build -tags=preview -ldflags "-X github.com/alreadyonapp/alreadyon/server/internal/httpapi.nativeBuildVersion=$$(TZ=America/New_York date +%y.%m.%d)_$$(git rev-parse --short=7 HEAD)" -o alreadyon-server ./cmd/server
	@cd web && npm run build
preview-login:
	@cd server && go run ./cmd/preview-account --reset
preview:
	@scripts/preview.sh start
preview-status:
	@scripts/preview.sh status
preview-stop:
	@scripts/preview.sh stop

# Legacy contributor commands, outside the shared maintainer workflow (see AGENTS.md).
.PHONY: deploy-local deploy-local-status deploy-local-stop local-release
deploy-local:
	@scripts/container.sh deploy
deploy-local-status:
	@scripts/container.sh status
deploy-local-stop:
	@scripts/container.sh stop
local-release: deploy-local test-e2e

# Legacy isolated CI helper; shared releases use make image then approved deployment
# only after the maintainer requests release.
.PHONY: ci-local
ci-local: lint
	@cd web && npx tsc -b --pretty false
	@CI_BASE=origin/dev python3 scripts/ci/check-documentation.py
	@python3 scripts/ci/check-syntax.py
	@go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
	@python3 -m unittest discover -s scripts/ci -p 'test_*.py'
	@$(MAKE) --no-print-directory test
	@$(MAKE) --no-print-directory local-release

# Optional maintainer operations stay in ignored local configuration.
-include .private/Makefile
