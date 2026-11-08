# syntax=docker/dockerfile:1
ARG GO_VERSION=1.27
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS build
WORKDIR /src
COPY go.work go.work.sum ./
COPY backend/go.mod backend/go.sum ./backend/
RUN --mount=type=cache,target=/go/pkg/mod go -C backend mod download
COPY backend ./backend
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go -C backend build -trimpath \
      -ldflags="-s -w -X github.com/ArionMiles/alfred/internal/buildinfo.Version=$VERSION" \
      -o /out/alfred ./cmd/alfred

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl git zsh \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --create-home --uid 1000 --shell /bin/zsh alfred \
    && mkdir -p /var/lib/alfred/workspace /etc/alfred \
    && chown -R alfred:alfred /var/lib/alfred
COPY --from=build /out/alfred /usr/local/bin/alfred
COPY deploy/config.toml /etc/alfred/config.toml
ENV HOME=/home/alfred XDG_CONFIG_HOME=/etc XDG_DATA_HOME=/var/lib \
    ALFRED_CONFIG_FILE=/etc/alfred/config.toml
USER alfred
WORKDIR /var/lib/alfred/workspace
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=5s --start-period=10s --retries=3 \
    CMD curl -fsS --max-time 3 -H 'Content-Type: application/json' -d '{}' \
      http://127.0.0.1:8080/alfred.v1.DaemonService/GetStatus | grep -q DAEMON_STATE_READY
ENTRYPOINT ["alfred"]
CMD ["serve"]
