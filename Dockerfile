# syntax=docker/dockerfile:1
# Source-only build: docker build -t artemis:local .
# Build stages run on the builder architecture; Go cross-compiles the backend.
ARG GO_VERSION=1.26.3
FROM --platform=$BUILDPLATFORM node:22-bookworm-slim AS frontend
ARG NPM_REGISTRY=https://registry.npmjs.org
ENV NEXT_TELEMETRY_DISABLED=1 HUSKY=0
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --registry="$NPM_REGISTRY"
COPY web/ ./
RUN npm run build:static

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS backend
ARG TARGETOS
ARG TARGETARCH
ARG GOPROXY=https://proxy.golang.org,direct
ARG ARTEX_BUILD_VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=frontend /src/web/out/ server/webui/dist/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -tags embedui -trimpath \
      -ldflags "-s -w -buildid= -X main.version=${ARTEX_BUILD_VERSION}" \
      -o /out/artemis ./cmd/artex

FROM node:22-bookworm-slim AS node-runtime
FROM python:3.12-slim-bookworm AS runtime
ARG NPM_REGISTRY=https://registry.npmjs.org
COPY --from=node-runtime /usr/local/bin/node /usr/local/bin/node
COPY --from=node-runtime /usr/local/lib/node_modules /usr/local/lib/node_modules
RUN ln -s ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
    && ln -s ../lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx \
    && apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates ripgrep curl wget vim git jq unzip \
      dnsutils iputils-ping netcat-openbsd inetutils-telnet whois nmap \
    && rm -rf /var/lib/apt/lists/*
# Keep browsers in an explicit image-owned location for both MCP and CLI.
ENV PLAYWRIGHT_BROWSERS_PATH=/opt/playwright
RUN npm install -g --registry="$NPM_REGISTRY" @playwright/mcp@latest @playwright/cli@latest playwright@latest \
    && playwright-cli --help \
    && playwright install --with-deps chromium \
    && npm cache clean --force \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=backend /out/artemis /app/artemis
COPY --chmod=755 start.sh /app/start.sh
COPY skills/ /app/skills/
COPY LICENSE THIRD_PARTY_NOTICES.md /app/
COPY web/LICENSE /app/licenses/web-LICENSE
COPY web/src/lib/fonts/assets/*LICENSE* /app/licenses/
# PostgreSQL lives in a separate service; recordings, JWT and workspaces persist here.
VOLUME ["/app/data"]
EXPOSE 8787 8788
HEALTHCHECK --interval=10s --timeout=5s --start-period=60s --retries=12 \
    CMD curl --fail --silent http://127.0.0.1:8787/api/auth/status > /dev/null || exit 1
ENTRYPOINT ["/app/start.sh"]
CMD ["-addr", ":8787", "-proxy", ":8788"]
