package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

const csrfTestHost = "https://admin.example.com"

func runCSRF(method string, headers map[string]string) *httptest.ResponseRecorder {
	mw := CSRFGuard(csrfTestHost)
	req := httptest.NewRequest(method, "/api/v1/accounts", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)
	return rec
}

func TestCSRFGuard_GETPassesThrough(t *testing.T) {
	if rec := runCSRF(http.MethodGet, nil); rec.Code != http.StatusOK {
		t.Errorf("GET should pass without Origin, got %d", rec.Code)
	}
}

func TestCSRFGuard_PostWithMatchingOriginAllowed(t *testing.T) {
	rec := runCSRF(http.MethodPost, map[string]string{"Origin": csrfTestHost})
	if rec.Code != http.StatusOK {
		t.Errorf("POST with matching Origin should pass, got %d", rec.Code)
	}
}

func TestCSRFGuard_PostWithWrongOriginDenied(t *testing.T) {
	rec := runCSRF(http.MethodPost, map[string]string{"Origin": "https://evil.example"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("wrong Origin: want 403, got %d", rec.Code)
	}
	var body struct{ Code string }
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if body.Code != "CSRF_DENIED" {
		t.Errorf("want code CSRF_DENIED, got %q", body.Code)
	}
}

func TestCSRFGuard_PostWithoutOriginAndWithoutReferer_Denied(t *testing.T) {
	rec := runCSRF(http.MethodPost, nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("no Origin and no Referer: want 403, got %d", rec.Code)
	}
}

func TestCSRFGuard_PostFallsBackToRefererOrigin(t *testing.T) {
	rec := runCSRF(http.MethodPost, map[string]string{
		"Referer": csrfTestHost + "/admin/accounts",
	})
	if rec.Code != http.StatusOK {
		t.Errorf("POST with matching Referer origin should pass, got %d", rec.Code)
	}
}

func TestCSRFGuard_PostWithRefererFromDifferentOriginDenied(t *testing.T) {
	rec := runCSRF(http.MethodPost, map[string]string{
		"Referer": "https://evil.example/some/path",
	})
	if rec.Code != http.StatusForbidden {
		t.Errorf("Referer from different origin: want 403, got %d", rec.Code)
	}
}

func TestCSRFGuard_TrailingSlashOnAdminHostNormalised(t *testing.T) {
	mw := CSRFGuard(csrfTestHost + "/")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts", nil)
	req.Header.Set("Origin", csrfTestHost) // no trailing slash from browser
	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("trailing slash on adminHost must be normalised; got %d", rec.Code)
	}
}

func TestCSRFGuard_NewPanicsOnEmptyAdminHost(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("CSRFGuard(\"\") must panic; got none")
		}
	}()
	_ = CSRFGuard("")
}

// TestCSRFGuard_CaseInsensitiveHostMatches — RFC 3986 says scheme/host are
// case-insensitive. An adminHost configured with mixed case must still match
// browser-sent (lowercased) Origin.
func TestCSRFGuard_CaseInsensitiveHostMatches(t *testing.T) {
	mw := CSRFGuard("https://Admin.Example.COM")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts", nil)
	req.Header.Set("Origin", "https://admin.example.com")
	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("uppercase host in adminHost must match lowercase Origin; got %d", rec.Code)
	}
}

// TestCSRFGuard_DefaultPortStripped — admin running on https:443 may be
// configured either with or without the explicit :443. Browser Origin always
// omits the default port; both forms must be treated as equivalent.
func TestCSRFGuard_DefaultPortStripped(t *testing.T) {
	mw := CSRFGuard("https://admin.example.com:443")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts", nil)
	req.Header.Set("Origin", "https://admin.example.com") // no port
	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("default :443 must be stripped; got %d", rec.Code)
	}
}

// TestCSRFGuard_ExplicitNonDefaultPortRequired — admin on a non-default
// port must require browser-sent Origin to include the same port.
func TestCSRFGuard_ExplicitNonDefaultPortRequired(t *testing.T) {
	mw := CSRFGuard("https://admin.example.com:8443")
	// Right port → allowed.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts", nil)
	req.Header.Set("Origin", "https://admin.example.com:8443")
	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("matching :8443 must pass; got %d", rec.Code)
	}
	// Missing port → denied (not the default).
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/accounts", nil)
	req2.Header.Set("Origin", "https://admin.example.com")
	rec2 := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Errorf("missing :8443 must deny; got %d", rec2.Code)
	}
}

// TestCSRFGuard_NullOriginRejected — sandboxed iframes / opaque origins
// send the literal string "null". Must be denied.
func TestCSRFGuard_NullOriginRejected(t *testing.T) {
	rec := runCSRF(http.MethodPost, map[string]string{"Origin": "null"})
	if rec.Code != http.StatusForbidden {
		t.Errorf("null Origin must deny; got %d", rec.Code)
	}
}

// TestCSRFGuard_NewPanicsOnMalformedAdminHost — scheme-less host is the
// most common deployment misconfiguration; we want a startup failure, not
// silent denial of every mutating request post-deploy.
func TestCSRFGuard_NewPanicsOnMalformedAdminHost(t *testing.T) {
	cases := []string{
		"admin.example.com",      // no scheme
		"https://",               // no host
		"://admin.example.com",   // empty scheme
		"not a url at all",       // garbage
	}
	for _, host := range cases {
		t.Run(host, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("CSRFGuard(%q) must panic; got none", host)
				}
			}()
			_ = CSRFGuard(host)
		})
	}
}

// All mutating methods are gated.
func TestCSRFGuard_AllMutatingMethodsDeniedWithoutOrigin(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			if rec := runCSRF(method, nil); rec.Code != http.StatusForbidden {
				t.Errorf("%s without Origin: want 403, got %d", method, rec.Code)
			}
		})
	}
}
