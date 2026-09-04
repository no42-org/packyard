/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

// Package github implements the GitHub OAuth provider for packyard-auth.
//
// Flow (RFC 6749 + PKCE RFC 7636):
//  1. AuthorizeURL builds /login/oauth/authorize?... with state + S256 code_challenge
//     and scopes user:email + read:org (D18 of the change).
//  2. Exchange POSTs to /login/oauth/access_token with code + code_verifier.
//  3. Fetches /user/emails and selects the row with primary=true AND
//     verified=true (D6 — verified primary email is the identity), lowercased.
//  4. Fetches /user/memberships/orgs/{org} to confirm membership; the
//     response's state field must be "active" for D7's first lock.
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/no42-org/packyard-auth/internal/auth"
)

// Default GitHub endpoints. Overridable via Config.BaseAPIURL /
// Config.BaseAuthURL for tests.
const (
	defaultAuthBaseURL = "https://github.com"
	defaultAPIBaseURL  = "https://api.github.com"
)

// Config captures everything the provider needs at runtime.
type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	// AllowedOrg is the GitHub organisation login (e.g. "no42-org") that
	// D7's first lock requires membership in.
	AllowedOrg string
	// BaseAuthURL defaults to https://github.com when zero.
	BaseAuthURL string
	// BaseAPIURL defaults to https://api.github.com when zero.
	BaseAPIURL string
	// HTTPClient defaults to http.DefaultClient when nil.
	HTTPClient *http.Client
}

// Provider satisfies auth.OAuthProvider for GitHub.
type Provider struct {
	cfg Config
}

// New returns a Provider with the given config. Missing endpoint URLs
// default to the public GitHub URLs; missing HTTPClient defaults to one
// with a 10-second per-request timeout so a hung provider can't pin a
// goroutine.
//
// Panics when required config is missing — the alternative (silently
// rejecting every login at runtime with a misleading "not a member" 403)
// is a worse operator experience.
func New(cfg Config) *Provider {
	if cfg.AllowedOrg == "" {
		panic("github.New: AllowedOrg is required; an empty value would silently reject every login")
	}
	if cfg.BaseAuthURL == "" {
		cfg.BaseAuthURL = defaultAuthBaseURL
	}
	if cfg.BaseAPIURL == "" {
		cfg.BaseAPIURL = defaultAPIBaseURL
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Provider{cfg: cfg}
}

func (p *Provider) Name() string { return auth.ProviderGitHub }

func (p *Provider) AuthorizeURL(state, codeChallenge string) string {
	q := url.Values{}
	q.Set("client_id", p.cfg.ClientID)
	q.Set("redirect_uri", p.cfg.RedirectURI)
	q.Set("response_type", "code")
	q.Set("scope", "user:email read:org")
	q.Set("state", state)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")
	return p.cfg.BaseAuthURL + "/login/oauth/authorize?" + q.Encode()
}

// tokenResponse is the subset of GitHub's token-exchange response we care about.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error,omitempty"`
}

type emailRow struct {
	Email    string `json:"email"`
	Primary  bool   `json:"primary"`
	Verified bool   `json:"verified"`
}

type userResponse struct {
	Login string `json:"login"`
}

type membershipResponse struct {
	State string `json:"state"` // "active" or "pending"
}

func (p *Provider) Exchange(ctx context.Context, code, codeVerifier string) (*auth.OAuthIdentity, error) {
	tok, err := p.exchangeToken(ctx, code, codeVerifier)
	if err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}

	email, err := p.fetchPrimaryVerifiedEmail(ctx, tok.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}

	user, err := p.fetchUser(ctx, tok.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}

	if err := p.requireOrgMembership(ctx, tok.AccessToken); err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}

	return &auth.OAuthIdentity{
		Email:          strings.ToLower(email),
		OrgMember:      true,
		ProviderUserID: user.Login,
		Provider:       auth.ProviderGitHub,
	}, nil
}

func (p *Provider) exchangeToken(ctx context.Context, code, codeVerifier string) (*tokenResponse, error) {
	body := url.Values{}
	body.Set("client_id", p.cfg.ClientID)
	body.Set("client_secret", p.cfg.ClientSecret)
	body.Set("code", code)
	body.Set("redirect_uri", p.cfg.RedirectURI)
	body.Set("code_verifier", codeVerifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.cfg.BaseAuthURL+"/login/oauth/access_token",
		strings.NewReader(body.Encode()))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", auth.ErrTokenExchange, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := p.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", auth.ErrTokenExchange, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d", auth.ErrTokenExchange, resp.StatusCode)
	}

	var tok tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return nil, fmt.Errorf("%w: decode response: %v", auth.ErrTokenExchange, err)
	}
	if tok.Error != "" || tok.AccessToken == "" {
		return nil, fmt.Errorf("%w: provider returned error %q", auth.ErrTokenExchange, tok.Error)
	}
	return &tok, nil
}

func (p *Provider) fetchPrimaryVerifiedEmail(ctx context.Context, token string) (string, error) {
	var rows []emailRow
	if err := p.apiGet(ctx, token, "/user/emails", &rows); err != nil {
		return "", err
	}
	for _, r := range rows {
		if r.Primary && r.Verified && r.Email != "" {
			return r.Email, nil
		}
	}
	// Distinguish "no email at all" from "primary not verified" for cleaner
	// audit signals; both still surface as ErrUnverifiedEmail at the caller.
	if len(rows) == 0 {
		return "", auth.ErrNoEmail
	}
	return "", auth.ErrUnverifiedEmail
}

func (p *Provider) fetchUser(ctx context.Context, token string) (*userResponse, error) {
	var u userResponse
	if err := p.apiGet(ctx, token, "/user", &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// requireOrgMembership returns nil only when the caller is an active member
// of cfg.AllowedOrg. The mapping:
//   - 200 with state=="active"   → nil (member)
//   - 200 with state=="pending"  → ErrNotOrgMember (invited but not yet joined)
//   - 404                        → ErrNotOrgMember (not a member at all)
//   - 403                        → ErrTokenExchange (token lacks read:org scope
//     or SAML SSO enforcement; this is a config
//     problem, not "you're not a member" — surfacing
//     it as the latter would mislead operators)
//   - anything else              → ErrTokenExchange (unexpected response)
func (p *Provider) requireOrgMembership(ctx context.Context, token string) error {
	path := "/user/memberships/orgs/" + url.PathEscape(p.cfg.AllowedOrg)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.BaseAPIURL+path, nil)
	if err != nil {
		return fmt.Errorf("%w: %v", auth.ErrTokenExchange, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := p.cfg.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", auth.ErrTokenExchange, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var m membershipResponse
		if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
			return fmt.Errorf("%w: decode membership: %v", auth.ErrTokenExchange, err)
		}
		if m.State != "active" {
			return auth.ErrNotOrgMember
		}
		return nil
	case http.StatusNotFound:
		return auth.ErrNotOrgMember
	case http.StatusForbidden:
		return fmt.Errorf("%w: 403 from membership endpoint (read:org scope missing or SAML SSO required)",
			auth.ErrTokenExchange)
	default:
		return fmt.Errorf("%w: unexpected status %d for org membership", auth.ErrTokenExchange, resp.StatusCode)
	}
}

// apiGet performs a GET against the API base with the given token and JSON-
// decodes the response into v. Surfaces ErrTokenExchange on transport / non-2xx
// because every API call here depends on the access token being good.
func (p *Provider) apiGet(ctx context.Context, token, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.BaseAPIURL+path, nil)
	if err != nil {
		return fmt.Errorf("%w: %v", auth.ErrTokenExchange, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := p.cfg.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", auth.ErrTokenExchange, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: GET %s -> %d", auth.ErrTokenExchange, path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		return fmt.Errorf("%w: decode %s: %v", auth.ErrTokenExchange, path, err)
	}
	return nil
}
