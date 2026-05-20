# ─── Stage 1: build ───────────────────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /src

# Copy workspace + all module manifests so go.work resolves correctly.
COPY go.work        go.work
COPY pkg/go.mod     pkg/go.mod
COPY panel/go.mod   panel/go.mod
COPY daemon/go.mod  daemon/go.mod

# Copy full source.
COPY pkg/   pkg/
COPY panel/ panel/

# go mod tidy: resolves workspace, creates go.sum.
WORKDIR /src/panel
RUN GONOSUMCHECK=github.com/aethernet/* go mod tidy

# Build the static binary.
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w" \
      -o /out/aether-panel \
      ./cmd/aether-panel

# ─── Stage 2: runtime ─────────────────────────────────────────────────────────
FROM alpine:3.20

RUN apk add --no-cache ca-certificates wget

RUN addgroup -S aether && adduser -S -G aether aether

COPY --from=builder /out/aether-panel /usr/local/bin/aether-panel

# Static web assets (the SPA: index.html, app.js, style.css).
COPY --chown=aether:aether panel/web /var/www/aethernet

USER aether
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/aether-panel"]
CMD ["--listen", "0.0.0.0:8080", "--static", "/var/www/aethernet", \
     "--daemon", "http://daemon:8081"]
