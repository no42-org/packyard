/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

package middleware

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
)

// nonceCtxKey is the context key used to surface the per-request CSP nonce to
// downstream handlers — specifically the /admin/* SPA `index.html` handler,
// which must template the same nonce into the <script>/<style> tags that the
// CSP header references. A typed key prevents collisions with other packages'
// context values.
type nonceCtxKey struct{}

// CSPNonceFromContext returns the per-request CSP nonce written by
// AdminCSP, or "" if the middleware did not run. The SPA handler MUST embed
// this nonce into every inline <script>/<style> tag in `index.html` —
// otherwise the browser will refuse to execute them under the configured
// policy.
func CSPNonceFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(nonceCtxKey{}).(string)
	return v
}

// AdminCSP is the Content-Security-Policy middleware for the admin SPA per
// D22 of change 2026-05-21-admin-ui-account-lifecycle § 7.7. It generates a
// 16-byte base64 nonce per request, attaches it to the request context for
// the downstream HTML handler to template into <script>/<style> tags, and
// writes the matching CSP header on the response.
//
// Policy:
//
//	default-src 'self';
//	script-src 'self' 'nonce-{nonce}';
//	style-src 'self' 'nonce-{nonce}';
//	frame-ancestors 'none';
//	object-src 'none';
//	base-uri 'self'
//
// The `'self'` source on script-src/style-src admits the bundled SPA assets;
// the nonce admits the per-request inline tags that the `index.html` handler
// renders (e.g. for runtime config injection). `frame-ancestors 'none'`
// blocks clickjacking; `object-src 'none'` blocks legacy plugin execution.
//
// Failure mode: if entropy is unavailable (extremely rare on Linux/Darwin),
// the middleware returns 500 rather than serving the page with a weak nonce.
func AdminCSP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw [16]byte
		if _, err := rand.Read(raw[:]); err != nil {
			// /admin/* is an HTML surface, not the JSON admin API — use a
			// plain-text error so browsers render it directly. The structured
			// {code, message} envelope applies to /api/v1/* only.
			http.Error(w, "failed to generate CSP nonce", http.StatusInternalServerError)
			return
		}
		// RawURLEncoding (no padding, '-' and '_' instead of '+' and '/') is
		// the CSP3-recommended form: it avoids the '/' that confuses path-
		// based tooling and the '=' padding that complicates quoting.
		nonce := base64.RawURLEncoding.EncodeToString(raw[:])

		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' 'nonce-"+nonce+"'; "+
				"style-src 'self' 'nonce-"+nonce+"'; "+
				"frame-ancestors 'none'; "+
				"object-src 'none'; "+
				"base-uri 'self'")

		ctx := context.WithValue(r.Context(), nonceCtxKey{}, nonce)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
