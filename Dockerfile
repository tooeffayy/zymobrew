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
# distroless/static has no pg_dump, so admin backups (which shell out to
# pg_dump) are non-functional in this image. Switch to a base with the
# postgres client when that pipeline goes live for self-hosters.
FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/zymo /usr/local/bin/zymo
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/zymo"]
CMD ["serve"]
