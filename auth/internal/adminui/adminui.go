/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

// Package adminui serves the embedded React + TypeScript SPA at /admin/*
// per D12 of change 2026-05-21-admin-ui-account-lifecycle. Static assets
// (the Vite-built bundle) are served straight from the embedded FS;
// the SPA shell at /admin/ returns the index.html with the per-request
// CSP nonce already set on the response header by middleware.AdminCSP.
//
// CSP authorisation: the policy is `script-src 'self'` + `style-src
// 'self'` (D22), which already authorises every bundled asset since
// they're all served same-origin from /admin/assets/. The per-request
// nonce header is set unconditionally so a future inline runtime-config
// `<script nonce="…">` block can be added without policy churn.
//
// Build dependency: the Vite build must populate `dist/` before the Go
// build embeds it. `make admin-ui` runs Vite; the Dockerfile multi-stage
// build invokes the Node toolchain first. A `dist/.gitkeep` placeholder
// keeps the directory present in source checkouts so `go build` doesn't
// fail before the SPA has been built — the handler returns a clear "SPA
// not built" message in that state instead of a confusing 404.
package adminui

import (
	"bytes"
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

const (
	indexHTMLPath  = "dist/index.html"
	assetURLPrefix = "/admin/assets/"
)

// placeholderHTML is served when dist/index.html is missing or empty — this
// happens before the first `make admin-ui` run. The message tells the
// developer exactly what to do; production never sees it because the
// Dockerfile builds the SPA before the Go binary.
const placeholderHTML = `<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><title>Packyard Admin</title></head>
<body><h1>Admin SPA not built</h1>
<p>The React+TypeScript SPA bundle is missing. Run <code>make admin-ui</code> to build it,
then rebuild the auth binary.</p></body></html>`

// Handler builds the /admin/* HTTP handler. It serves:
//
//   - GET /admin/                       → index.html (SPA shell)
//   - GET /admin/<spa-route>            → index.html (SPA owns routing)
//   - GET /admin/assets/<hashed-name>   → static asset from embedded FS
//
// The handler MUST be mounted under a middleware.AdminCSP wrapper so the
// CSP header (with per-request nonce) is set on every response.
func Handler(logger *slog.Logger) http.Handler {
	staticFS, err := fs.Sub(distFS, "dist")
	if err != nil {
		// `dist/` is guaranteed to exist by the //go:embed directive — if
		// fs.Sub fails at runtime, something has gone very wrong.
		logger.Error("admin-ui: fs.Sub failed", slog.String("error", err.Error()))
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "admin UI subsystem failed to initialise", http.StatusInternalServerError)
		})
	}

	assetServer := http.StripPrefix("/admin/", http.FileServer(http.FS(staticFS)))
	indexBytes := readIndexHTML(logger)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Asset routing: only paths that resolve under /admin/assets/ (after
		// path.Clean) are served from the embed. This guards against URLs
		// like /admin/assets/../index.html which path.Clean resolves to
		// /admin/index.html — those used to be served by the file server
		// with the year-long immutable cache header, poisoning the SPA shell
		// at every CDN.
		if strings.HasPrefix(r.URL.Path, assetURLPrefix) {
			cleaned := path.Clean(r.URL.Path)
			if !strings.HasPrefix(cleaned, assetURLPrefix) {
				// Traversal escaped the assets prefix — refuse rather than
				// silently serve a non-asset.
				http.NotFound(w, r)
				return
			}
			// Wrap the response writer so the immutable cache header only
			// lands on 2xx responses. A 404 for a missing asset must not be
			// cached for a year.
			cw := &cacheOn2xx{ResponseWriter: w, cacheHeader: "public, max-age=31536000, immutable"}
			assetServer.ServeHTTP(cw, r)
			return
		}

		// Everything else (including unknown deep links) returns the SPA
		// shell so the client-side router can resolve the route.
		serveIndex(w, indexBytes)
	})
}

func readIndexHTML(logger *slog.Logger) []byte {
	data, err := fs.ReadFile(distFS, indexHTMLPath)
	if err != nil || len(bytes.TrimSpace(data)) == 0 {
		logger.Warn("admin-ui: dist/index.html missing or empty — serving placeholder. Run `make admin-ui`.",
			slog.Any("error", err))
		return []byte(placeholderHTML)
	}
	return data
}

func serveIndex(w http.ResponseWriter, indexBytes []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// SPA shell is small + per-request; don't cache so a deploy picks up
	// the new asset references immediately.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(indexBytes)
}

// cacheOn2xx is a ResponseWriter that sets the long-lived immutable
// Cache-Control header only when the wrapped handler writes a 2xx
// status. Non-2xx responses (404s for missing assets, etc.) get whatever
// cache headers the wrapped handler set — by default nothing.
type cacheOn2xx struct {
	http.ResponseWriter
	cacheHeader string
}

func (c *cacheOn2xx) WriteHeader(status int) {
	if status >= 200 && status < 300 {
		c.ResponseWriter.Header().Set("Cache-Control", c.cacheHeader)
	}
	c.ResponseWriter.WriteHeader(status)
}
