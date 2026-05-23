// Package microsoft implements the Microsoft Entra (Azure AD) OAuth provider.
//
// Per D3 + D6 of change 2026-05-21-admin-ui-account-lifecycle, the provider
// is tenant-bound: the `tid` claim of the returned id_token must equal the
// configured tenant id. When that gate passes, the `email` (or
// `preferred_username` fallback) claim is trusted "by issuer" — we accept it
// without an additional Graph API round-trip because it came from a
// TLS-validated Microsoft endpoint.
//
// JWT signature verification against the JWKS endpoint is intentionally NOT
// performed here; per D3 the trust boundary is the TLS-validated token
// exchange itself. If a stricter posture is needed (compliance regimes,
// federated SAML hand-offs) the verification belongs in a separate change.
package microsoft

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/no42-org/packyard-auth/internal/auth"
)

const defaultAuthorityBaseURL = "https://login.microsoftonline.com"

// Config captures everything the Microsoft provider needs.
type Config struct {
	TenantID     string
	ClientID     string
	ClientSecret string
	RedirectURI  string
	// AuthorityBaseURL defaults to https://login.microsoftonline.com.
	AuthorityBaseURL string
	HTTPClient       *http.Client
}

// Provider satisfies auth.OAuthProvider for Microsoft Entra.
type Provider struct {
	cfg Config
}

// New returns a Provider with the given config. AuthorityBaseURL defaults to
// the public commercial Entra endpoint; HTTPClient defaults to a client with
// a 10-second per-request timeout so a hung provider can't pin a goroutine.
//
// Panics when required config is missing.
func New(cfg Config) *Provider {
	if cfg.TenantID == "" {
		panic("microsoft.New: TenantID is required for tenant-bound OAuth (D3)")
	}
	if cfg.AuthorityBaseURL == "" {
		cfg.AuthorityBaseURL = defaultAuthorityBaseURL
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Provider{cfg: cfg}
}

func (p *Provider) Name() string { return auth.ProviderMicrosoft }

func (p *Provider) AuthorizeURL(state, codeChallenge string) string {
	q := url.Values{}
	q.Set("client_id", p.cfg.ClientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", p.cfg.RedirectURI)
	q.Set("response_mode", "query")
	q.Set("scope", "openid email profile")
	q.Set("state", state)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")
	return p.cfg.AuthorityBaseURL + "/" + url.PathEscape(p.cfg.TenantID) +
		"/oauth2/v2.0/authorize?" + q.Encode()
}

type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	IDToken          string `json:"id_token"`
	TokenType        string `json:"token_type"`
	Error            string `json:"error,omitempty"`
	ErrorDescription string `json:"error_description,omitempty"`
}

// idTokenClaims holds the claims we extract from the id_token JWT. Per D3
// JWT signature verification is intentionally skipped (token came from a
// TLS-validated authority); the claims below are validated explicitly.
//
// `aud` (audience) — must equal our client_id, otherwise an attacker who
// obtained a valid token for a different Entra app could replay it here
// (audience-confusion).
// `iss` (issuer)   — must equal the tenant-specific issuer, otherwise a
// token from a different tenant could be presented.
// `exp` / `nbf`    — Unix timestamps; reject expired or not-yet-valid tokens.
// `tid`            — must equal our configured tenant (D3 first lock); GUIDs
// are case-insensitive in Entra so the comparison uses EqualFold.
// `email_verified` — per D3 absent is acceptable for tenant-bound tokens;
// we reject only when the claim is explicitly false. Documented intent.
type idTokenClaims struct {
	TenantID          string `json:"tid"`
	Audience          string `json:"aud"`
	Issuer            string `json:"iss"`
	ExpiresAt         int64  `json:"exp"`
	NotBefore         int64  `json:"nbf"`
	Email             string `json:"email"`
	PreferredUsername string `json:"preferred_username"`
	EmailVerified     *bool  `json:"email_verified,omitempty"`
}

func (p *Provider) Exchange(ctx context.Context, code, codeVerifier string) (*auth.OAuthIdentity, error) {
	tok, err := p.exchangeToken(ctx, code, codeVerifier)
	if err != nil {
		return nil, fmt.Errorf("microsoft: %w", err)
	}
	if tok.IDToken == "" {
		return nil, fmt.Errorf("microsoft: %w", auth.ErrTokenExchange)
	}

	claims, err := decodeIDTokenClaims(tok.IDToken)
	if err != nil {
		return nil, fmt.Errorf("microsoft: %w: %v", auth.ErrTokenExchange, err)
	}

	if err := p.validateClaims(claims); err != nil {
		return nil, fmt.Errorf("microsoft: %w", err)
	}

	email := claims.Email
	if email == "" {
		email = claims.PreferredUsername
	}
	if email == "" {
		return nil, fmt.Errorf("microsoft: %w", auth.ErrNoEmail)
	}
	if claims.EmailVerified != nil && !*claims.EmailVerified {
		return nil, fmt.Errorf("microsoft: %w", auth.ErrUnverifiedEmail)
	}

	return &auth.OAuthIdentity{
		Email:          strings.ToLower(email),
		OrgMember:      true, // tid + iss + aud match is the membership signal
		ProviderUserID: email,
		Provider:       auth.ProviderMicrosoft,
	}, nil
}

// validateClaims enforces the JWT-claim invariants the spec relies on for
// trust without a signature check. See idTokenClaims doc for the rationale
// of each branch.
func (p *Provider) validateClaims(c *idTokenClaims) error {
	if c.TenantID == "" || !strings.EqualFold(c.TenantID, p.cfg.TenantID) {
		return auth.ErrNotOrgMember
	}
	if c.Audience != p.cfg.ClientID {
		return fmt.Errorf("%w: id_token audience %q does not match configured client_id",
			auth.ErrTokenExchange, c.Audience)
	}
	expectedIssuer := p.expectedIssuer()
	if c.Issuer != expectedIssuer {
		return fmt.Errorf("%w: id_token issuer %q does not match expected %q",
			auth.ErrTokenExchange, c.Issuer, expectedIssuer)
	}
	// RFC 7519 §4.1.4 lists `exp` as REQUIRED-when-present and forbids
	// processing past expiry. After JSON-decode into int64 we cannot
	// distinguish "claim omitted" from "claim was literally 0"; either case
	// must be treated as invalid, not as "skip the expiry check". The same
	// reasoning applies to `nbf`: a missing/zero value must fail closed
	// rather than admit the token unconditionally.
	if c.ExpiresAt <= 0 {
		return fmt.Errorf("%w: id_token is missing or has invalid exp claim", auth.ErrTokenExchange)
	}
	now := time.Now().Unix()
	if now > c.ExpiresAt {
		return fmt.Errorf("%w: id_token is expired", auth.ErrTokenExchange)
	}
	if c.NotBefore < 0 {
		return fmt.Errorf("%w: id_token has invalid nbf claim", auth.ErrTokenExchange)
	}
	if c.NotBefore > 0 && now < c.NotBefore {
		return fmt.Errorf("%w: id_token is not yet valid", auth.ErrTokenExchange)
	}
	return nil
}

// expectedIssuer returns the tenant-specific v2.0 issuer URL Microsoft Entra
// uses when AuthorityBaseURL is the public endpoint. Override-friendly: a
// test or sovereign-cloud deployment that points AuthorityBaseURL elsewhere
// gets a matching issuer expectation.
func (p *Provider) expectedIssuer() string {
	return p.cfg.AuthorityBaseURL + "/" + p.cfg.TenantID + "/v2.0"
}

func (p *Provider) exchangeToken(ctx context.Context, code, codeVerifier string) (*tokenResponse, error) {
	body := url.Values{}
	body.Set("client_id", p.cfg.ClientID)
	body.Set("client_secret", p.cfg.ClientSecret)
	body.Set("code", code)
	body.Set("redirect_uri", p.cfg.RedirectURI)
	body.Set("grant_type", "authorization_code")
	body.Set("code_verifier", codeVerifier)
	body.Set("scope", "openid email profile")

	endpoint := p.cfg.AuthorityBaseURL + "/" + url.PathEscape(p.cfg.TenantID) +
		"/oauth2/v2.0/token"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
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
	if tok.Error != "" {
		return nil, fmt.Errorf("%w: %s: %s", auth.ErrTokenExchange, tok.Error, tok.ErrorDescription)
	}
	return &tok, nil
}

// decodeIDTokenClaims parses the claims segment of a JWT without verifying
// the signature. Per D3, signature verification is intentionally skipped
// because the token was just received from a TLS-validated authority
// endpoint; the only check needed is that the claims belong to the
// configured tenant (enforced by the caller).
func decodeIDTokenClaims(idToken string) (*idTokenClaims, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return nil, errors.New("id_token does not have three JWT segments")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}
	var claims idTokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("unmarshal claims: %w", err)
	}
	return &claims, nil
}
