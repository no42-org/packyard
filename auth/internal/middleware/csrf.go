/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

package middleware

import (
	"net/http"
	"net/url"
	"strings"
)

// originParts is a normalised scheme/host/port triple used to compare
// browser-supplied Origin strings against the configured admin host. RFC 3986
// makes scheme and host case-insensitive and a missing port equivalent to the
// scheme's default port; this struct collapses both so byte-equal comparison
// after normalisation is correct.
type originParts struct {
	scheme string
	host   string
	port   string // empty when matching the scheme default
}

// parseOrigin returns the normalised parts of an origin string. ok is false
// when the input is missing a scheme or host (which a real browser Origin
// never is — sandboxed iframes send "null" which we also reject here).
// Path/query/fragment on the input are tolerated and stripped — Referer
// (which we fall back to when Origin is absent) legitimately carries them.
func parseOrigin(s string) (originParts, bool) {
	if s == "" || s == "null" {
		return originParts{}, false
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return originParts{}, false
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	// Collapse default ports so https://admin:443 == https://admin and
	// http://admin:80 == http://admin (browsers strip them from Origin).
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	return originParts{scheme: scheme, host: host, port: port}, true
}

// parseAdminHostStrict is the startup-only variant of parseOrigin. It
// additionally rejects values that carry a non-trivial path, query, or
// fragment, since a configured admin host of
// "https://admin.example.com/admin/" is a misconfiguration that would
// silently corrupt CSRF semantics. A bare-root trailing slash
// (`https://admin.example.com/`) is tolerated and normalised away — that
// shape is common in shell-pasted env values. main.go's validAdminHost
// also rejects path-bearing values; this is defense in depth.
func parseAdminHostStrict(s string) (originParts, bool) {
	parts, ok := parseOrigin(s)
	if !ok {
		return originParts{}, false
	}
	if u, err := url.Parse(s); err == nil {
		if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
			return originParts{}, false
		}
	}
	return parts, true
}

// CSRFGuard returns middleware that rejects mutating requests (POST/PUT/
// PATCH/DELETE) whose Origin header — or Referer, when Origin is absent —
// does not match adminHost. Per D15 this is a defense-in-depth layer on top
// of the SameSite=Strict session cookie: a request that bypasses Same-Site
// browser semantics (e.g. via misconfigured proxy, custom client) still has
// to carry a matching Origin.
//
// adminHost must be a full origin URL ("https://admin.pkg.example.org");
// scheme + host are required. Construction panics on empty / malformed
// adminHost so the failure surfaces at startup rather than silently 403'ing
// every mutating request after deploy.
//
// GET/HEAD/OPTIONS are passed through unchanged.
func CSRFGuard(adminHost string) func(http.Handler) http.Handler {
	if adminHost == "" {
		panic("middleware.CSRFGuard: adminHost is required (D15)")
	}
	expected, ok := parseAdminHostStrict(adminHost)
	if !ok {
		panic("middleware.CSRFGuard: adminHost must be a bare scheme://host URL (no path/query/fragment), got " + adminHost)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}

			rawOrigin := r.Header.Get("Origin")
			if rawOrigin == "" {
				// Some browsers omit Origin on same-origin form posts;
				// fall back to Referer per D15.
				rawOrigin = r.Header.Get("Referer")
			}
			got, ok := parseOrigin(rawOrigin)
			if !ok || got != expected {
				writeError(w, http.StatusForbidden, "CSRF_DENIED",
					"request Origin/Referer does not match the configured admin host")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
