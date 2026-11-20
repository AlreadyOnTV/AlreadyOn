# Toolchain

[![AlreadyOn](../web/public/branding/header-doc.svg)](../README.md)

The production baseline is defined by [the runtime Dockerfile](../deploy/Dockerfile), [build base](../deploy/base.Dockerfile), [CI base](../deploy/ci-base.Dockerfile), `server/go.mod` and `web/package.json`. Current build recipes use Go 1.27, Node 26 and Ubuntu 26.04.

Run `make check-toolchain` for consistency checks. `make check-ubuntu-lts` additionally requires network access to inspect the upstream Ubuntu baseline. A native workstation may differ; validate release behavior in the production image.

Use `npm ci` with the committed lockfile and normal Go module tooling. Do not replace dependencies or toolchain versions opportunistically during unrelated work. Explain upgrades and run affected checks.

SQLite schema changes use versioned migrations. Preserve existing data and test upgrades from supported older schemas. Avoid destructive migration shortcuts.

Android uses its committed Gradle wrapper and the versions in its build files; see [Android](ANDROID.md). FFmpeg build configuration and patches are maintained under `deploy/` and must retain their licensing and runtime checks.

## Validation caches

A read-only main-branch workflow warms runtime layers and Gradle caches without signing or publishing; its daily runtime build bypasses the cache to refresh upstream packages. GitHub isolates PR caches by merge ref, while PRs may read default-branch caches. Gradle's build cache applies to unsigned APK assembly; unit tests and lint are not task-cached.

BuildKit invalidates layers against Dockerfiles, upstream images and copied inputs. Layer exports do not include `RUN --mount=type=cache` contents. CI and application images use the local Docker builder because the containerized BuildKit builder cannot see images loaded only into the host daemon. Go module and build caches are mounted into the source-test container; test results are cleared before each run. The CI image already supplies Chromium matched to the web lockfile.

Source-test logs report dependency, test, fixture and server-startup timings. The browser harness shares mutable server state and is not sharded. Benchmark image archive compression and separate runtime-test jobs against transfer and runner cost before changing them.

The next candidate uses Compose BOM 2026.11.01, Compose compiler plugin 2.4.21 and UI Automator 2.4.1. Check both apps' unit tests and instrumentation-source compilation together. Go migrations use Goose 3.30.1. For npm 12.2.3, verify the locked replacement dependency tree with clean installs and CLI smoke checks in the CI environment.
