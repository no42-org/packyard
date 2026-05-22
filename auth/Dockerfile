# Stage 1: SPA build — Vite + TypeScript + React per D12. Output is copied
# into auth/internal/adminui/dist where the Go build's //go:embed picks it
# up.
FROM node:20-alpine AS spa-builder
WORKDIR /spa
# Lockfile is committed and authoritative — `npm ci` fails closed when it
# disagrees with package.json, so the container build can't silently drift.
COPY internal/admin-ui/package.json internal/admin-ui/package-lock.json ./
RUN npm ci
COPY internal/admin-ui/ ./
RUN npm run build

# Stage 2: Go build — embeds the freshly-built SPA bundle.
FROM golang:1.26.3-alpine AS builder
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
