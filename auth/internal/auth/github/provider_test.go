/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

package github

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/no42-org/packyard-auth/internal/auth"
)

// roundTripFunc lets a test swap in a canned HTTP transport.
type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// route maps an inbound URL to a canned response body + status.
type route struct {
	status int
	body   any
}

// canned builds an http.Client whose transport returns the matched route or
// a 500 if the URL is unmatched.
func canned(routes map[string]route) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		key := req.Method + " " + req.URL.Path
		r, ok := routes[key]
		if !ok {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       io.NopCloser(strings.NewReader("no route: " + key)),
				Header:     make(http.Header),
			}, nil
		}
		var bodyReader io.Reader
		switch v := r.body.(type) {
		case string:
			bodyReader = strings.NewReader(v)
		default:
			b, _ := json.Marshal(v)
			bodyReader = strings.NewReader(string(b))
		}
		return &http.Response{
			StatusCode: r.status,
			Body:       io.NopCloser(bodyReader),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}, nil
	})}
}

func newTestProvider(routes map[string]route) *Provider {
	return New(Config{
		ClientID:     "test-client",
		ClientSecret: "secret",
		RedirectURI:  "https://admin.example/api/v1/auth/callback/github",
		AllowedOrg:   "no42-org",
		BaseAuthURL:  "https://github.test",
		BaseAPIURL:   "https://api.github.test",
		HTTPClient:   canned(routes),
	})
}

func TestProvider_Name(t *testing.T) {
	p := New(Config{AllowedOrg: "no42-org"})
	if p.Name() != "github" {
		t.Errorf("Name: want github, got %q", p.Name())
	}
}

func TestProvider_New_PanicsWithoutAllowedOrg(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when AllowedOrg is empty; got none")
		}
	}()
	_ = New(Config{ClientID: "abc"})
}

func TestProvider_AuthorizeURL_ContainsRequiredParams(t *testing.T) {
	p := New(Config{ClientID: "abc", RedirectURI: "https://r/cb", BaseAuthURL: "https://github.test", AllowedOrg: "no42-org"})
	got := p.AuthorizeURL("state-val", "challenge-val")
	for _, want := range []string{
		"https://github.test/login/oauth/authorize?",
		"client_id=abc",
		"code_challenge=challenge-val",
		"code_challenge_method=S256",
		"redirect_uri=https%3A%2F%2Fr%2Fcb",
		"response_type=code",
		"scope=user%3Aemail+read%3Aorg",
		"state=state-val",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("AuthorizeURL missing %q in %q", want, got)
		}
	}
}

func TestProvider_Exchange_HappyPath(t *testing.T) {
	p := newTestProvider(map[string]route{
		"POST /login/oauth/access_token": {200, map[string]any{
			"access_token": "tok-1",
			"token_type":   "bearer",
			"scope":        "user:email read:org",
		}},
		"GET /user/emails": {200, []map[string]any{
			{"email": "noise@example.com", "primary": false, "verified": true},
			{"email": "Operator@Example.COM", "primary": true, "verified": true},
		}},
		"GET /user":                           {200, map[string]any{"login": "octocat"}},
		"GET /user/memberships/orgs/no42-org": {200, map[string]any{"state": "active"}},
	})

	id, err := p.Exchange(context.Background(), "code", "verifier")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if id.Email != "operator@example.com" {
		t.Errorf("Email: want lowercase canonical, got %q", id.Email)
	}
	if id.ProviderUserID != "octocat" {
		t.Errorf("ProviderUserID: want octocat, got %q", id.ProviderUserID)
	}
	if !id.OrgMember {
		t.Errorf("OrgMember: want true")
	}
}

func TestProvider_Exchange_UnverifiedEmail(t *testing.T) {
	p := newTestProvider(map[string]route{
		"POST /login/oauth/access_token": {200, map[string]any{"access_token": "tok"}},
		"GET /user/emails": {200, []map[string]any{
			{"email": "x@x.com", "primary": true, "verified": false},
		}},
	})
	_, err := p.Exchange(context.Background(), "code", "verifier")
	if !errors.Is(err, auth.ErrUnverifiedEmail) {
		t.Errorf("want ErrUnverifiedEmail, got %v", err)
	}
}

func TestProvider_Exchange_NoEmails(t *testing.T) {
	p := newTestProvider(map[string]route{
		"POST /login/oauth/access_token": {200, map[string]any{"access_token": "tok"}},
		"GET /user/emails":               {200, []map[string]any{}},
	})
	_, err := p.Exchange(context.Background(), "code", "verifier")
	if !errors.Is(err, auth.ErrNoEmail) {
		t.Errorf("want ErrNoEmail, got %v", err)
	}
}

func TestProvider_Exchange_NotOrgMember(t *testing.T) {
	p := newTestProvider(map[string]route{
		"POST /login/oauth/access_token": {200, map[string]any{"access_token": "tok"}},
		"GET /user/emails": {200, []map[string]any{
			{"email": "x@x.com", "primary": true, "verified": true},
		}},
		"GET /user": {200, map[string]any{"login": "outsider"}},
		// 404 from membership endpoint = not a member; provider must surface
		// this as ErrNotOrgMember so the callback can map it to 403.
		"GET /user/memberships/orgs/no42-org": {404, ""},
	})
	_, err := p.Exchange(context.Background(), "code", "verifier")
	if !errors.Is(err, auth.ErrNotOrgMember) {
		t.Fatalf("want ErrNotOrgMember, got %v", err)
	}
}

func TestProvider_Exchange_PendingMembershipNotMember(t *testing.T) {
	p := newTestProvider(map[string]route{
		"POST /login/oauth/access_token": {200, map[string]any{"access_token": "tok"}},
		"GET /user/emails": {200, []map[string]any{
			{"email": "x@x.com", "primary": true, "verified": true},
		}},
		"GET /user":                           {200, map[string]any{"login": "invited"}},
		"GET /user/memberships/orgs/no42-org": {200, map[string]any{"state": "pending"}},
	})
	_, err := p.Exchange(context.Background(), "code", "verifier")
	if !errors.Is(err, auth.ErrNotOrgMember) {
		t.Fatalf("pending invite must surface as ErrNotOrgMember, got %v", err)
	}
}

func TestProvider_Exchange_403IsConfigError(t *testing.T) {
	// 403 means the token lacks read:org scope OR SAML SSO needs to be
	// satisfied — both are config errors, not "not a member".
	p := newTestProvider(map[string]route{
		"POST /login/oauth/access_token": {200, map[string]any{"access_token": "tok"}},
		"GET /user/emails": {200, []map[string]any{
			{"email": "x@x.com", "primary": true, "verified": true},
		}},
		"GET /user":                           {200, map[string]any{"login": "someone"}},
		"GET /user/memberships/orgs/no42-org": {403, ""},
	})
	_, err := p.Exchange(context.Background(), "code", "verifier")
	if !errors.Is(err, auth.ErrTokenExchange) {
		t.Fatalf("403 must be classified as ErrTokenExchange (config error), got %v", err)
	}
}

func TestProvider_Exchange_TokenExchangeError(t *testing.T) {
	p := newTestProvider(map[string]route{
		"POST /login/oauth/access_token": {200, map[string]any{
			"error": "bad_verification_code",
		}},
	})
	_, err := p.Exchange(context.Background(), "code", "verifier")
	if !errors.Is(err, auth.ErrTokenExchange) {
		t.Errorf("want ErrTokenExchange, got %v", err)
	}
}
