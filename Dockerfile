# Multi-stage build for pushObserver
# Stage 1: build static binary
FROM golang:1.23-alpine AS builder

RUN apk add --no-cache git

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /push-observer ./cmd/push-observer

# Stage 2: minimal runtime
FROM alpine:3.21

RUN apk add --no-cache \
    git \
    openssh-client \
    docker-cli \
    docker-compose \
    ca-certificates \
    tzdata

# Create non-root user
RUN adduser -D -h /home/webhook webhook
USER webhook
WORKDIR /home/webhook

# Create .ssh directory for deploy keys
RUN mkdir -p /home/webhook/.ssh && chmod 700 /home/webhook/.ssh

COPY --from=builder /push-observer /usr/local/bin/push-observer

EXPOSE 9090
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:9090/health || exit 1

ENTRYPOINT ["push-observer"]
