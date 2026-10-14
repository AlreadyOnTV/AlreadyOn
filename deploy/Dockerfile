# Build application layers on reusable local or GHCR bases. The version arguments stay
# here so local toolchain checks and the published base images share one baseline.
ARG GO_VERSION=1.27
ARG NODE_VERSION=24
ARG UBUNTU_VERSION=26.04
ARG CI_BASE=ghcr.io/alreadyontv/alreadyon-base:base-ci
ARG RUNTIME_BASE=ghcr.io/alreadyontv/alreadyon-base:base-runtime
FROM ${CI_BASE} AS ci-base

FROM ci-base AS server-build
USER root
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY server/ .
# Runtime paths are fixed at link time; -s -w removes linker/debug tables that
# the application does not read while retaining normal Go panic stack traces.
ARG LDFLAGS="-X github.com/alreadyonapp/alreadyon/server/internal/config.dataDir=/config \
    -X github.com/alreadyonapp/alreadyon/server/internal/config.webDir=/usr/local/share/alreadyon/web \
    -X github.com/alreadyonapp/alreadyon/server/internal/httpapi.openAPISpecPath=/usr/local/share/alreadyon/openapi.yaml"
ENV CGO_ENABLED=0 GOAMD64=v1
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags "$LDFLAGS -s -w" -o /out/alreadyon-server ./cmd/server

FROM ci-base AS web-build
USER root
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY web/ .
RUN npm run build

FROM ${RUNTIME_BASE} AS runtime
COPY --from=server-build /out/alreadyon-server /usr/local/bin/
RUN for command in access-key clear-browser-sessions reset-password tls; do \
    ln -s alreadyon-server "/usr/local/bin/alreadyon-$command"; done
# Built-in web assets and API documentation are immutable image content. Only
# the SQLite database and uploaded profile icons live in the /config volume.
COPY --from=web-build /src/web/dist /usr/local/share/alreadyon/web
COPY api/openapi.yaml /usr/local/share/alreadyon/openapi.yaml
COPY deploy/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod 0755 /usr/local/bin/entrypoint.sh

ENV TZ=America/New_York \
    PUID=1000 \
    PGID=1000
# Required build inputs invalidate the version stamp even when application
# compile layers are reusable.
ARG VCS_REF
ARG BUILD_TIMESTAMP
RUN test -n "$BUILD_TIMESTAMP" && echo "$VCS_REF" | grep -Eq '^[0-9a-f]{40}$' \
    && printf '%s_%.7s\n' "$(TZ=America/New_York date +%y.%m.%d)" "$VCS_REF" > /usr/local/share/alreadyon/build-version
LABEL org.opencontainers.image.source="https://github.com/AlreadyOnTV/AlreadyOn" \
    org.opencontainers.image.revision=$VCS_REF
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --retries=3 \
    CMD alreadyon-tls health || exit 1
VOLUME ["/config"]
EXPOSE 19900 19902
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
