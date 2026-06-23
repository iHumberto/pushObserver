# Multi-stage build for pushObserver
# Stage 1: build static binary
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git upx

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /push-observer ./cmd/push-observer && \
    upx --best --lzma /push-observer

# Stage 2: minimal runtime
FROM alpine:3.21

# Build deps: curl (download compose), upx (compress compose) — removed after
RUN apk add --no-cache --virtual .build-deps curl upx && \
    apk add --no-cache \
        git \
        openssh-client \
        ca-certificates && \
    # Download docker-compose v2 (Go binary, works on musl)
    ARCH=$(uname -m) && \
    COMPOSE_VERSION="v2.33.1" && \
    curl -sSL "https://github.com/docker/compose/releases/download/${COMPOSE_VERSION}/docker-compose-linux-${ARCH}" \
        -o /usr/local/bin/docker-compose && \
    chmod +x /usr/local/bin/docker-compose && \
    # Compress with UPX — typically 50-60% reduction for Go binaries
    upx --best --lzma /usr/local/bin/docker-compose && \
    /usr/local/bin/docker-compose version && \
    # Remove build deps and docs to save space
    apk del .build-deps && \
    rm -rf /usr/share/man /usr/share/doc /usr/share/info /var/cache/apk/*

# Create non-root user and home directory structure
# IMPORTANT: mkdir and chown run as ROOT so ownership is guaranteed correct.
# If chown runs as USER webhook, it silently fails — only root can change ownership.
RUN adduser -D -h /home/webhook webhook \
    && mkdir -p /home/webhook/.ssh /home/webhook/.config /home/webhook/repos \
    && chmod 700 /home/webhook/.ssh \
    && chown -R webhook:webhook /home/webhook

USER webhook
WORKDIR /home/webhook

# Bundle default config template — entrypoint copies it on first run
COPY --chown=webhook:webhook push-observer.yaml /home/webhook/push-observer.yaml.default

# Entrypoint: auto-creates config on first run, then execs binary
COPY --chown=webhook:webhook docker-entrypoint.sh /usr/local/bin/
COPY --from=builder /push-observer /usr/local/bin/push-observer

# Static assets (i18n, favicon, etc.) — needed at runtime for the web dashboard
COPY --chown=webhook:webhook assets/ /home/webhook/assets/

EXPOSE 9090
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null http://localhost:9090/health || exit 1

ENTRYPOINT ["docker-entrypoint.sh"]
