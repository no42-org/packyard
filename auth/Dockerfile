# Stage 1: SPA build — Vite + TypeScript + React per D12. Output is copied
# into auth/internal/adminui/dist where the Go build's //go:embed picks it
# up.
#
# TODO(release-hardening): pin base images to immutable digests (node@sha256:…,
# golang@sha256:…, distroless@sha256:…) before tagging a production release.
# Mutable tags would otherwise let a compromised upstream image silently land
# in the next build.
FROM node:26-alpine@sha256:2d984a15c9b54fd0aeb608b8e0d0d83529eb34d2966db27a1fb4f1edc3d298a3 AS spa-builder
WORKDIR /spa
# Lockfile is committed and authoritative — `npm ci` fails closed when it
# disagrees with package.json, so the container build can't silently drift.
# `--ignore-scripts` blocks postinstall hooks from transitive deps so a
# compromised dep can't run code in the build stage (the stage is discarded
# after build, but the blast radius still touches the build runner).
COPY internal/admin-ui/package.json internal/admin-ui/package-lock.json ./
RUN npm ci --ignore-scripts
COPY internal/admin-ui/ ./
RUN npm run build

# Stage 2: Go build — embeds the freshly-built SPA bundle.
FROM golang:1.27.1-alpine@sha256:cf6fca6641884b8433441b2b0652976f975e1d0fdd26d177eaaf8596087f3125 AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Overlay the SPA bundle from stage 1 into the embed directory before
# compiling so //go:embed all:dist captures the real assets, not the
# placeholder .gitkeep.
COPY --from=spa-builder /spa/dist/ ./internal/adminui/dist/
RUN CGO_ENABLED=0 GOOS=linux go build -o /auth ./cmd/server

FROM gcr.io/distroless/static-debian12@sha256:d75cdd72874d4790092fcb1b058493ecf6bb5bf2b2b897045b00ff01d91843f2
COPY --from=builder /auth /auth
ENTRYPOINT ["/auth"]
