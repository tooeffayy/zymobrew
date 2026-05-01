# syntax=docker/dockerfile:1

# --- Web build ----------------------------------------------------------
# Bun produces /web/dist, which the Go stage below copies into the
# embed tree so //go:embed all:dist picks up the production bundle
# instead of the .gitkeep-only placeholder shipped in git.
FROM oven/bun:1-alpine AS web
WORKDIR /web

# Copy the manifest first so the (slow) install step caches as long as
# package.json hasn't changed. The lockfile glob covers Bun's modern
# text-based bun.lock and the older binary bun.lockb; BuildKit accepts
# zero matches, so a fresh checkout without a committed lockfile still
# builds (the install layer regenerates one).
COPY web/package.json web/bun.lock* web/bun.lockb* ./
RUN bun install --no-progress

# Now the rest of the SPA source. Layer cache invalidates only when
# something under web/ (other than package.json) changes.
COPY web/ ./
RUN bun run build

# --- Go build -----------------------------------------------------------
# go.mod pins 1.25.7; the Dockerfile must match or `go build` rejects
# the module's go directive.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# Replace the placeholder web/dist (only .gitkeep on disk) with the
# freshly-built SPA before compiling. Done after `COPY . .` so the
# committed placeholder doesn't shadow the real bundle.
COPY --from=web /web/dist ./web/dist

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/zymo ./cmd/zymo

# --- Runtime ------------------------------------------------------------
# Alpine + postgresql-client so admin-backup dispatcher's pg_dump shellout
# resolves on PATH. We keep the binary statically linked (CGO_ENABLED=0
# above) so libc divergence between alpine and the build stage is
# irrelevant to the Go side; alpine's musl matters only for the postgres
# client binaries it ships, which apk handles.
#
# Pinning the postgres version: pg_dump is forward-compatible (a newer
# pg_dump can dump from an older server), so postgresql16-client works
# against the project's Postgres 14+ requirement.
FROM alpine:3.20
RUN apk add --no-cache ca-certificates postgresql16-client \
    && addgroup -S -g 65532 nonroot \
    && adduser  -S -u 65532 -G nonroot nonroot
COPY --from=build /out/zymo /usr/local/bin/zymo
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/zymo"]
CMD ["serve"]
