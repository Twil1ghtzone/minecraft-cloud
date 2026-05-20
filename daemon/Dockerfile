# ─── Stage 1: build ──────────────────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /src

# Copy module files first for layer caching.
COPY pkg/go.mod      pkg/go.mod
COPY daemon/go.mod   daemon/go.mod
COPY go.work         go.work

# Download deps before copying source (cache layer).
WORKDIR /src/pkg
RUN go mod download

WORKDIR /src/daemon
RUN go mod download

# Copy full source.
WORKDIR /src
COPY pkg/    pkg/
COPY daemon/ daemon/

# Build the binary.
WORKDIR /src/daemon
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w" \
      -o /out/aether-daemon \
      ./cmd/aether-daemon

# ─── Stage 2: runtime ────────────────────────────────────────────────────────
FROM alpine:3.20

# wget used in healthcheck; ca-certificates for HTTPS to registries
RUN apk add --no-cache ca-certificates wget

# Non-root user for the daemon process
RUN addgroup -S aether && adduser -S -G aether aether

COPY --from=builder /out/aether-daemon /usr/local/bin/aether-daemon

# Config is provided via environment variables or a mounted config file.
RUN mkdir -p /var/lib/aethernet /etc/aethernet && \
    chown -R aether:aether /var/lib/aethernet /etc/aethernet

USER aether
EXPOSE 7000 7001 8081 2022

ENTRYPOINT ["/usr/local/bin/aether-daemon"]
CMD ["--config", "/etc/aethernet/daemon.yaml", "--bootstrap"]
