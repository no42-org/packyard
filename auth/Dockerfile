# Stage 1: SPA build — Vite + TypeScript + React per D12. Output is copied
# into auth/internal/adminui/dist where the Go build's //go:embed picks it
# up.
#
# TODO(release-hardening): pin base images to immutable digests (node@sha256:…,
# golang@sha256:…, distroless@sha256:…) before tagging a production release.
# Mutable tags would otherwise let a compromised upstream image silently land
# in the next build.
FROM node:26-alpine AS spa-builder
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
FROM golang:1.27.0-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Overlay the SPA bundle from stage 1 into the embed directory before
# compiling so //go:embed all:dist captures the real assets, not the
# placeholder .gitkeep.
COPY --from=spa-builder /spa/dist/ ./internal/adminui/dist/
RUN CGO_ENABLED=0 GOOS=linux go build -o /auth ./cmd/server

FROM gcr.io/distroless/static-debian12
COPY --from=builder /auth /auth
ENTRYPOINT ["/auth"]
