package microsoft

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/no42-org/packyard-auth/internal/auth"
)

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// mintIDToken builds a fake JWT with the supplied claims. Signature segment
// is left as the literal "sig" since the provider doesn't verify it (D3).
// Defaults aud/iss/exp to values consistent with newTestProvider so tests
// only need to set the claim they're exercising.
func mintIDToken(claims map[string]any) string {
	if _, ok := claims["aud"]; !ok {
		claims["aud"] = "client"
	}
	if _, ok := claims["iss"]; !ok {
		claims["iss"] = "https://login.microsoftonline.test/tenant-123/v2.0"
	}
	if _, ok := claims["exp"]; !ok {
		claims["exp"] = time.Now().Add(time.Hour).Unix()
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256"}`))
	body, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(body)
	return header + "." + payload + ".sig"
}

func cannedTokenResponse(idToken string, errCode string) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := map[string]any{
			"access_token": "atk",
			"token_type":   "Bearer",
			"id_token":     idToken,
		}
		if errCode != "" {
			body = map[string]any{"error": errCode, "error_description": "denied"}
		}
		b, _ := json.Marshal(body)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(string(b))),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}, nil
	})}
}

func newTestProvider(client *http.Client) *Provider {
	return New(Config{
		TenantID:         "tenant-123",
		ClientID:         "client",
		ClientSecret:     "secret",
		RedirectURI:      "https://admin.example/api/v1/auth/callback/microsoft",
		AuthorityBaseURL: "https://login.microsoftonline.test",
		HTTPClient:       client,
	})
}

func TestProvider_Name(t *testing.T) {
	if New(Config{TenantID: "abc"}).Name() != "microsoft" {
		t.Errorf("Name mismatch")
	}
}

func TestProvider_New_PanicsWithoutTenantID(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when TenantID is empty; got none")
		}
	}()
	_ = New(Config{ClientID: "abc"})
}

func TestProvider_AuthorizeURL_TenantInPath(t *testing.T) {
	p := New(Config{TenantID: "abc", ClientID: "cid", RedirectURI: "https://r/cb",
		AuthorityBaseURL: "https://login.microsoftonline.test"})
	got := p.AuthorizeURL("state-v", "chall-v")
	if !strings.Contains(got, "/abc/oauth2/v2.0/authorize?") {
		t.Errorf("AuthorizeURL missing tenant in path: %q", got)
	}
	for _, want := range []string{
		"client_id=cid", "code_challenge=chall-v",
		"code_challenge_method=S256", "state=state-v",
		"scope=openid+email+profile",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("AuthorizeURL missing %q in %q", want, got)
		}
	}
}

func TestProvider_Exchange_HappyPath(t *testing.T) {
	idToken := mintIDToken(map[string]any{
		"tid":            "tenant-123",
		"email":          "Operator@Example.COM",
		"email_verified": true,
	})
	p := newTestProvider(cannedTokenResponse(idToken, ""))
	id, err := p.Exchange(context.Background(), "code", "verifier")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if id.Email != "operator@example.com" {
		t.Errorf("Email lower-cased: got %q", id.Email)
	}
	if !id.OrgMember {
		t.Errorf("OrgMember should be true on tid match")
	}
}

func TestProvider_Exchange_TenantMismatch(t *testing.T) {
	idToken := mintIDToken(map[string]any{
		"tid":   "different-tenant",
		"email": "x@x.com",
	})
	p := newTestProvider(cannedTokenResponse(idToken, ""))
	_, err := p.Exchange(context.Background(), "code", "verifier")
	if !errors.Is(err, auth.ErrNotOrgMember) {
		t.Errorf("want ErrNotOrgMember on tid mismatch, got %v", err)
	}
}

func TestProvider_Exchange_NoEmail(t *testing.T) {
	idToken := mintIDToken(map[string]any{"tid": "tenant-123"})
	p := newTestProvider(cannedTokenResponse(idToken, ""))
	_, err := p.Exchange(context.Background(), "code", "verifier")
	if !errors.Is(err, auth.ErrNoEmail) {
		t.Errorf("want ErrNoEmail, got %v", err)
	}
}

func TestProvider_Exchange_PreferredUsernameFallback(t *testing.T) {
	idToken := mintIDToken(map[string]any{
		"tid":                "tenant-123",
		"preferred_username": "Alice@Example.COM",
	})
	p := newTestProvider(cannedTokenResponse(idToken, ""))
	id, err := p.Exchange(context.Background(), "code", "verifier")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if id.Email != "alice@example.com" {
		t.Errorf("preferred_username fallback: got %q", id.Email)
	}
}

func TestProvider_Exchange_TokenError(t *testing.T) {
	p := newTestProvider(cannedTokenResponse("", "invalid_grant"))
	_, err := p.Exchange(context.Background(), "code", "verifier")
	if !errors.Is(err, auth.ErrTokenExchange) {
		t.Errorf("want ErrTokenExchange, got %v", err)
	}
}

func TestProvider_Exchange_EmailVerifiedFalse(t *testing.T) {
	idToken := mintIDToken(map[string]any{
		"tid":            "tenant-123",
		"email":          "x@x.com",
		"email_verified": false,
	})
	p := newTestProvider(cannedTokenResponse(idToken, ""))
	_, err := p.Exchange(context.Background(), "code", "verifier")
	if !errors.Is(err, auth.ErrUnverifiedEmail) {
		t.Errorf("want ErrUnverifiedEmail, got %v", err)
	}
}

func TestProvider_Exchange_TenantIDCaseInsensitive(t *testing.T) {
	// Provider config uses "tenant-123"; id_token returns "TENANT-123".
	idToken := mintIDToken(map[string]any{
		"tid":   "TENANT-123",
		"email": "ok@x.com",
	})
	p := newTestProvider(cannedTokenResponse(idToken, ""))
	if _, err := p.Exchange(context.Background(), "code", "verifier"); err != nil {
		t.Errorf("case-insensitive tid should match; got %v", err)
	}
}

func TestProvider_Exchange_RejectsWrongAudience(t *testing.T) {
	idToken := mintIDToken(map[string]any{
		"tid":   "tenant-123",
		"aud":   "someone-elses-client", // overrides default
		"email": "x@x.com",
	})
	p := newTestProvider(cannedTokenResponse(idToken, ""))
	_, err := p.Exchange(context.Background(), "code", "verifier")
	if !errors.Is(err, auth.ErrTokenExchange) {
		t.Errorf("want ErrTokenExchange on audience mismatch, got %v", err)
	}
}

func TestProvider_Exchange_RejectsWrongIssuer(t *testing.T) {
	idToken := mintIDToken(map[string]any{
		"tid":   "tenant-123",
		"iss":   "https://attacker.example/issuer",
		"email": "x@x.com",
	})
	p := newTestProvider(cannedTokenResponse(idToken, ""))
	_, err := p.Exchange(context.Background(), "code", "verifier")
	if !errors.Is(err, auth.ErrTokenExchange) {
		t.Errorf("want ErrTokenExchange on issuer mismatch, got %v", err)
	}
}

func TestProvider_Exchange_RejectsExpiredToken(t *testing.T) {
	idToken := mintIDToken(map[string]any{
		"tid":   "tenant-123",
		"exp":   time.Now().Add(-time.Hour).Unix(),
		"email": "x@x.com",
	})
	p := newTestProvider(cannedTokenResponse(idToken, ""))
	_, err := p.Exchange(context.Background(), "code", "verifier")
	if !errors.Is(err, auth.ErrTokenExchange) {
		t.Errorf("want ErrTokenExchange on expired token, got %v", err)
	}
}
