/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

package middleware

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminCSP_SetsHeaderAndNonce(t *testing.T) {
	var seenNonce string
	h := AdminCSP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenNonce = CSPNonceFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if seenNonce == "" {
		t.Error("downstream handler did not see a CSP nonce in context")
	}
	csp := w.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("Content-Security-Policy header not set")
	}
	if !strings.Contains(csp, "'nonce-"+seenNonce+"'") {
		t.Errorf("CSP header missing nonce directive: %s", csp)
	}
	// D22 directives — assert each is present so a future refactor that drops
	// one fails loudly.
	for _, directive := range []string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self'",
		"frame-ancestors 'none'",
		"object-src 'none'",
		"base-uri 'self'",
	} {
		if !strings.Contains(csp, directive) {
			t.Errorf("CSP header missing directive %q: %s", directive, csp)
		}
	}
}

func TestAdminCSP_NoncesAreUniquePerRequest(t *testing.T) {
	h := AdminCSP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test-Nonce", CSPNonceFromContext(r.Context()))
		w.WriteHeader(http.StatusOK)
	}))

	w1 := httptest.NewRecorder()
	h.ServeHTTP(w1, httptest.NewRequest(http.MethodGet, "/admin/", nil))
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/admin/", nil))

	n1 := w1.Header().Get("X-Test-Nonce")
	n2 := w2.Header().Get("X-Test-Nonce")
	if n1 == "" || n2 == "" {
		t.Fatalf("nonces should be set, got %q / %q", n1, n2)
	}
	if n1 == n2 {
		t.Errorf("two requests produced the same nonce: %s", n1)
	}
}

func TestCSPNonceFromContext_NoNonceReturnsEmpty(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	if got := CSPNonceFromContext(req.Context()); got != "" {
		t.Errorf("unexpected nonce on bare request context: %q", got)
	}
}

// TestAdminCSP_NonceIsSixteenBytesBase64Url protects D22's "16 random
// bytes base64" requirement against future weakenings (e.g. a copy-paste
// regression to `var raw [4]byte` or a switch to a fixed sentinel).
func TestAdminCSP_NonceIsSixteenBytesBase64Url(t *testing.T) {
	var seen string
	h := AdminCSP(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = CSPNonceFromContext(r.Context())
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/admin/", nil))

	if seen == "" {
		t.Fatal("nonce should be set")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(seen)
	if err != nil {
		t.Fatalf("nonce should decode as RawURLEncoding base64: %v (got %q)", err, seen)
	}
	if len(decoded) != 16 {
		t.Errorf("nonce should decode to 16 bytes, got %d", len(decoded))
	}
	// Refuse padding / non-URL chars — RawURLEncoding output never contains
	// '+', '/', or '='. A regression to StdEncoding fails this check.
	if strings.ContainsAny(seen, "+/=") {
		t.Errorf("nonce contains StdEncoding chars (+, /, =): %q", seen)
	}
}
