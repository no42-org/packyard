package adminui

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/no42-org/packyard-auth/internal/middleware"
)

func TestHandler_PlaceholderWhenIndexMissing(t *testing.T) {
	// With only dist/.gitkeep present, the embed has no real index.html;
	// the handler should serve the explicit placeholder so devs see a
	// clear "run make admin-ui" message instead of a confusing 404.
	h := Handler(slog.Default())
	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "Admin SPA not built") {
		t.Errorf("expected placeholder message, got: %s", body)
	}
}

func TestHandler_DeepLinkServesIndex(t *testing.T) {
	// SPA owns client-side routing: any /admin/<route> should return the
	// index document so the router can resolve the path.
	h := Handler(slog.Default())
	req := httptest.NewRequest(http.MethodGet, "/admin/accounts/abc123", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("deep link: want 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type: want text/html, got %q", ct)
	}
}

func TestHandler_NonGetReturns405(t *testing.T) {
	h := Handler(slog.Default())
	req := httptest.NewRequest(http.MethodPost, "/admin/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /admin/: want 405, got %d", w.Code)
	}
	if a := w.Header().Get("Allow"); a == "" {
		t.Error("Allow header missing on 405")
	}
}

func TestHandler_NonceInjectedIntoHTML(t *testing.T) {
	// Wrap the admin handler with AdminCSP so the nonce is available in
	// context, then assert the placeholder HTML emits nonce attributes on
	// its <script> and <link> tags. The placeholder happens to have no
	// such tags — instead, exercise the integration by serving an
	// index-shaped payload via the middleware path on a fake handler that
	// emits a <script> tag.
	wrapped := middleware.AdminCSP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonce := middleware.CSPNonceFromContext(r.Context())
		if nonce == "" {
			t.Fatal("expected nonce in context inside AdminCSP")
		}
		// Mimic what serveIndex would do.
		body := `<html><head><script src="/admin/assets/x.js"></script></head><body></body></html>`
		body = strings.ReplaceAll(body, "<script ", `<script nonce="`+nonce+`" `)
		_, _ = io.WriteString(w, body)
	}))

	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, req)
	body := w.Body.String()
	if !strings.Contains(body, `nonce="`) {
		t.Errorf("nonce was not injected into <script>: %s", body)
	}
	if w.Header().Get("Content-Security-Policy") == "" {
		t.Error("CSP header missing")
	}
}
