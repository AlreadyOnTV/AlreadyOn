# Locked npm build tooling

The CI image keeps Node 24 and npm 11.21.0. npm's published archive bundles older vulnerable dependencies, so the image resolves a replacement dependency tree with `npm ci` using this checked-in manifest and lockfile. The npm CLI source, version and license notices stay from the upstream npm archive.

`package.json` comes from npm 11.21.0's manifest with development scripts/dependencies removed. Its explicit overrides select patched, same-major versions of brace-expansion, http-cache-semantics and undici. This affects CI/build tooling only; the application runtime does not ship npm.

Review package compatibility and advisory fixes before changing overrides. Regenerate the lock with `npm install --prefix deploy/npm-tooling --package-lock-only --ignore-scripts --no-audit --no-fund`. Both files are part of the CI base recipe fingerprint. No package is globally suppressed or hand-edited inside a running image.

Validate npm installation, the WebUI build, Chromium launch and relevant Go checks in the rebuilt CI image. Rescan that exact image and retain private evidence before publication. npm major upgrades require separate review of lifecycle-script and build behavior.

The October 18 batch advances spdx-expression-parse to 5, read to 6, ssri to 14, make-fetch-happen to 16 and promise-spawn to 10. These are npm CLI dependencies, not application dependencies. Their engine floors require Node 24.15.0 or newer in the Node 24 line. Validate the replacement CLI in the recipe-built CI base, including install/build and networking, before accepting the batch.
