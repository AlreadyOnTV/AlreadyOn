# Build/test tooling is separate so browser changes never rebuild FFmpeg.
ARG GO_VERSION=1.27
ARG NODE_VERSION=24
ARG UBUNTU_VERSION=26.04
ARG RUNTIME_BASE=ghcr.io/alreadyonapp/alreadyon-base:base-runtime
FROM golang:${GO_VERSION} AS go-toolchain
FROM node:${NODE_VERSION} AS node-toolchain
# The CI base adds only tools needed to compile the application and run the full
# Go/Chromium suites; these build and test tools never enter runtime images.
FROM ${RUNTIME_BASE} AS ci-base
COPY --from=node-toolchain /usr/local/ /usr/local/
COPY --from=go-toolchain /usr/local/go /usr/local/go
ENV PATH=/usr/local/go/bin:/home/ubuntu/go/bin:$PATH \
    PLAYWRIGHT_BROWSERS_PATH=/opt/playwright
# Pin refreshed npm tooling independently of Node; do not patch nested dependencies by hand.
# Only npm is used by the checked-in build; remove unused package-manager shims.
COPY deploy/npm-tooling/package.json deploy/npm-tooling/package-lock.json /tmp/npm-tooling/
COPY deploy/check-npm-tooling.js /tmp/check-npm-tooling.js
RUN npm install --global npm@11.21.0 \
    && npm ci --prefix /tmp/npm-tooling --ignore-scripts --no-audit --no-fund \
    && rm -rf /usr/local/lib/node_modules/npm/node_modules \
    && mv /tmp/npm-tooling/node_modules /usr/local/lib/node_modules/npm/node_modules \
    && node /tmp/check-npm-tooling.js /tmp/npm-tooling/package.json \
    && rm -rf /tmp/npm-tooling /tmp/check-npm-tooling.js \
    && rm -rf /usr/local/lib/node_modules/corepack \
    && rm -f /usr/local/bin/corepack /usr/local/bin/pnpm /usr/local/bin/pnpx /usr/local/bin/yarn /usr/local/bin/yarnpkg \
    && apt-get update && apt-get install -y --no-install-recommends \
    make python3 python3-yaml sqlite3 procps xvfb xauth \
    && rm -rf /var/lib/apt/lists/*
COPY web/package.json web/package-lock.json /tmp/browser-install/
RUN cd /tmp/browser-install && npm ci \
    && npx playwright install --with-deps --only-shell chromium \
    && chmod -R a+rX /opt/playwright \
    && rm -rf /tmp/browser-install /var/lib/apt/lists/* /root/.npm
WORKDIR /workspaces/alreadyon
CMD ["sleep", "infinity"]
