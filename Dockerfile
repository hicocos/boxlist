# syntax=docker/dockerfile:1.7
# Integrated edition: build custom frontend + native Go backend, one runtime process.
ARG NODE_IMAGE=node:24.21.0-bookworm-slim
ARG GO_IMAGE=golang:1.27.0-bookworm
ARG RUNTIME_IMAGE=debian:bookworm-slim
FROM ${NODE_IMAGE} AS frontend
WORKDIR /src
ENV HUSKY=0
RUN apt-get update && apt-get install -y --no-install-recommends git ca-certificates && rm -rf /var/lib/apt/lists/* \
    && npm install --global pnpm@11.24.0
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY patches/ ./patches/
RUN pnpm install --frozen-lockfile
COPY . .
RUN VITE_API_URL=/ pnpm run build

FROM ${GO_IMAGE} AS backend
WORKDIR /src/backend
ENV GOTOOLCHAIN=local CGO_ENABLED=0
# WebP encoding is pure Go (nativewebp); no Python or libwebp encoder service.
COPY backend/go.mod backend/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY backend/ ./
# Never fetch another frontend or run upstream build.sh: embed THIS repository's UI.
RUN rm -rf public/dist
COPY --from=frontend /src/dist/ ./public/dist/
ARG VERSION=custom-local
ARG SOURCE_COMMIT=unknown
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -tags=jsoniter -ldflags="-s -w -X github.com/OpenListTeam/OpenList/v4/internal/conf.Version=${VERSION} -X github.com/OpenListTeam/OpenList/v4/internal/conf.WebVersion=${VERSION} -X github.com/OpenListTeam/OpenList/v4/internal/conf.GitCommit=${SOURCE_COMMIT}" -o /out/openlist .

FROM ${RUNTIME_IMAGE} AS runtime
ARG VERSION=custom-local
ARG SOURCE_COMMIT=unknown
LABEL org.opencontainers.image.title="OpenList Custom — native integrated edition" \
      org.opencontainers.image.source="https://github.com/hicocos/openlist-frontend-custom" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${SOURCE_COMMIT}" \
      org.opencontainers.image.licenses="AGPL-3.0-only AND MIT"
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 1001 openlist \
    && useradd --uid 1001 --gid 1001 --no-create-home --shell /usr/sbin/nologin openlist \
    && install -d -o 1001 -g 1001 -m 0750 /opt/openlist/data
WORKDIR /opt/openlist
COPY --from=backend --chmod=755 /out/openlist ./openlist
COPY --from=frontend /src/dist/ ./dist/
COPY LICENSE /usr/share/licenses/openlist-custom/frontend-MIT.txt
COPY backend/LICENSE /usr/share/licenses/openlist-custom/backend-AGPL-3.0.txt
USER 1001:1001
VOLUME ["/opt/openlist/data"]
EXPOSE 5244
STOPSIGNAL SIGTERM
ENTRYPOINT ["/opt/openlist/openlist"]
CMD ["server", "--no-prefix"]
