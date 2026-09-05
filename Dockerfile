# syntax=docker/dockerfile:1@sha256:ecfaec9ed6d810b56388c508f4121597bfbba70d41a6dfeee4d8cad5f295fc32

# --- Web build ----------------------------------------------------------
# Bun produces /web/dist, which the Go stage below copies into the
# embed tree so //go:embed all:dist picks up the production bundle
# instead of the .gitkeep-only placeholder shipped in git.
FROM oven/bun:1-alpine@sha256:d888c0ae6c86d7866ff10c5aafdd9077b36aee6455b33dd270fb93c0dd5cef6f AS web
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
FROM golang:1.26-alpine@sha256:ce864e7223ac17b1775e6fd0b4c0db580c2eb50e7953a427916379e4b92a1628 AS build
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
FROM alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b
RUN apk add --no-cache ca-certificates postgresql16-client \
    && addgroup -S -g 65532 nonroot \
    && adduser  -S -u 65532 -G nonroot nonroot
COPY --from=build /out/zymo /usr/local/bin/zymo
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/zymo"]
CMD ["serve"]
