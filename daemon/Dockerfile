# ─── Stage 1: build ───────────────────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /src

# Copy workspace + all module manifests so go.work resolves correctly.
COPY go.work        go.work
COPY pkg/go.mod     pkg/go.mod
COPY daemon/go.mod  daemon/go.mod
COPY panel/go.mod   panel/go.mod

# Copy full source before tidy (tidy needs imports to detect missing deps).
COPY pkg/    pkg/
COPY daemon/ daemon/

# go mod tidy: adds missing deps (prometheus etc.), prunes unused ones,
# creates go.sum — all in one step. GONOSUMCHECK skips sum DB for private pkgs.
WORKDIR /src/daemon
RUN GONOSUMCHECK=github.com/aethernet/* go mod tidy

# Build the static binary.
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w" \
      -o /out/aether-daemon \
      ./cmd/aether-daemon

# ─── Stage 2: runtime ─────────────────────────────────────────────────────────
FROM alpine:3.20

RUN apk add --no-cache ca-certificates wget

RUN addgroup -S aether && adduser -S -G aether aether

COPY --from=builder /out/aether-daemon /usr/local/bin/aether-daemon

RUN mkdir -p /var/lib/aethernet /etc/aethernet && \
    chown -R aether:aether /var/lib/aethernet /etc/aethernet

USER aether
EXPOSE 7000 7001 8081 2022

ENTRYPOINT ["/usr/local/bin/aether-daemon"]
CMD ["--config", "/etc/aethernet/daemon.yaml", "--bootstrap"]
