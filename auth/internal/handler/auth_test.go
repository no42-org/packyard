package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/no42-org/packyard-auth/internal/audit"
	"github.com/no42-org/packyard-auth/internal/auth"
	"github.com/no42-org/packyard-auth/internal/middleware"
	"github.com/no42-org/packyard-auth/internal/store"
)

func TestAuth_Logout_DeletesSession(t *testing.T) {
	s, err := store.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	ctx := context.Background()
	op, _ := s.AllowlistOperator(ctx, "op@example.com", store.OperatorRoleAdmin, "bootstrap")
	sess, _ := s.CreateSession(ctx, op.ID, "127.0.0.1", "go-test")

	rec := &recordingLogoutAuditor{}
	h := NewAuthHandler(s, rec, slog.Default())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: middleware.SessionCookieName, Value: sess.ID})
	req = req.WithContext(auth.WithOperator(req.Context(), auth.Operator{ID: op.ID, Role: auth.RoleAdmin}))
	w := httptest.NewRecorder()
	h.Logout(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d: %s", w.Code, w.Body.String())
	}

	// Session must be deleted.
	if _, err := s.GetSession(ctx, sess.ID); !errors.Is(err, store.ErrSessionNotFound) {
		t.Errorf("session should be deleted; got %v", err)
	}

	// Cookie must be cleared.
	cookies := w.Result().Cookies()
	var foundCleared bool
	for _, c := range cookies {
		if c.Name == middleware.SessionCookieName && c.MaxAge < 0 {
			foundCleared = true
		}
	}
	if !foundCleared {
		t.Errorf("expected Set-Cookie that clears %s, got %v", middleware.SessionCookieName, cookies)
	}

	// Audit row written with the spec-mandated action name "logout".
	if len(rec.entries) != 1 || rec.entries[0].Action != "logout" {
		t.Errorf("expected one logout audit row, got %+v", rec.entries)
	}
}

func TestAuth_Logout_NoCookieIdempotent(t *testing.T) {
	s, err := store.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	rec := &recordingLogoutAuditor{}
	h := NewAuthHandler(s, rec, slog.Default())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	w := httptest.NewRecorder()
	h.Logout(w, req)
	if w.Code != http.StatusNoContent {
		t.Errorf("want 204 (idempotent logout), got %d", w.Code)
	}
	// No real session was deleted → no audit row (avoids anonymous-logout
	// pollution of the audit trail).
	if len(rec.entries) != 0 {
		t.Errorf("no-session logout should not write an audit row; got %+v", rec.entries)
	}
}

type recordingLogoutAuditor struct {
	entries []audit.Entry
}

func (r *recordingLogoutAuditor) Write(_ context.Context, e audit.Entry) {
	r.entries = append(r.entries, e)
}

// ─── Login / callback tests ─────────────────────────────────────────────────

// fakeProvider satisfies auth.OAuthProvider for handler tests.
type fakeProvider struct {
	name         string
	authURLFn    func(state, challenge string) string
	exchangeFn   func(ctx context.Context, code, verifier string) (*auth.OAuthIdentity, error)
}

func (f *fakeProvider) Name() string { return f.name }
func (f *fakeProvider) AuthorizeURL(state, challenge string) string {
	if f.authURLFn != nil {
		return f.authURLFn(state, challenge)
	}
	return "https://idp.test/authorize?state=" + state
}
func (f *fakeProvider) Exchange(ctx context.Context, code, verifier string) (*auth.OAuthIdentity, error) {
	return f.exchangeFn(ctx, code, verifier)
}

func newAuthHandlerWithProvider(t *testing.T, prov *fakeProvider) (*AuthHandler, *store.SQLiteStore, *recordingLogoutAuditor) {
	t.Helper()
	s, err := store.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	rec := &recordingLogoutAuditor{}
	h := &AuthHandler{
		Sessions:  s,
		Operators: s,
		Providers: map[string]auth.OAuthProvider{prov.name: prov},
		State:     auth.NewMemStateStore(context.Background(), time.Hour),
		Auditor:   rec,
		Logger:    slog.Default(),
	}
	return h, s, rec
}

func chiProviderReq(req *http.Request, providerName string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("provider", providerName)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func TestAuth_Login_UnknownProviderReturns404(t *testing.T) {
	h, _, _ := newAuthHandlerWithProvider(t, &fakeProvider{name: "github"})
	req := chiProviderReq(httptest.NewRequest(http.MethodGet, "/api/v1/auth/login/unknown", nil), "unknown")
	w := httptest.NewRecorder()
	h.Login(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
}

func TestAuth_Login_SetsHandleCookieAndRedirects(t *testing.T) {
	prov := &fakeProvider{
		name:      "github",
		authURLFn: func(state, ch string) string { return "https://idp.test/authorize?state=" + state + "&challenge=" + ch },
	}
	h, _, _ := newAuthHandlerWithProvider(t, prov)
	req := chiProviderReq(httptest.NewRequest(http.MethodGet, "/api/v1/auth/login/github", nil), "github")
	w := httptest.NewRecorder()
	h.Login(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("want 302, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://idp.test/authorize?state=") {
		t.Errorf("Location not as expected: %q", loc)
	}
	var foundHandle bool
	for _, c := range w.Result().Cookies() {
		if c.Name == "packyard_oauth_handle" && c.Value != "" {
			foundHandle = true
			if c.SameSite != http.SameSiteLaxMode {
				t.Errorf("handle cookie SameSite must be Lax (cross-site IdP redirect); got %v", c.SameSite)
			}
			if !c.HttpOnly || !c.Secure {
				t.Errorf("handle cookie missing HttpOnly/Secure flags")
			}
		}
	}
	if !foundHandle {
		t.Error("login should set a packyard_oauth_handle cookie")
	}
}

// runCallback performs the full login → callback dance, using the cookie
// from login and the state captured from the redirect URL.
func runCallback(t *testing.T, h *AuthHandler, prov *fakeProvider, code string) *httptest.ResponseRecorder {
	t.Helper()

	// Step 1 — login to obtain handle cookie + state.
	loginReq := chiProviderReq(httptest.NewRequest(http.MethodGet, "/api/v1/auth/login/"+prov.name, nil), prov.name)
	loginRec := httptest.NewRecorder()
	h.Login(loginRec, loginReq)
	loc := loginRec.Header().Get("Location")
	state := extractQueryParam(loc, "state")
	if state == "" {
		t.Fatal("could not extract state from login redirect")
	}

	// Step 2 — callback with the cookie + matching state.
	cbReq := chiProviderReq(
		httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback/"+prov.name+"?state="+state+"&code="+code, nil),
		prov.name,
	)
	for _, c := range loginRec.Result().Cookies() {
		cbReq.AddCookie(c)
	}
	cbRec := httptest.NewRecorder()
	h.Callback(cbRec, cbReq)
	return cbRec
}

// extractQueryParam pulls a single query-param value out of a URL string
// without dragging in the full net/url machinery.
func extractQueryParam(u, key string) string {
	idx := strings.Index(u, key+"=")
	if idx < 0 {
		return ""
	}
	rest := u[idx+len(key)+1:]
	if i := strings.IndexByte(rest, '&'); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

func TestAuth_Callback_HappyPath(t *testing.T) {
	prov := &fakeProvider{
		name: "github",
		exchangeFn: func(_ context.Context, _, _ string) (*auth.OAuthIdentity, error) {
			return &auth.OAuthIdentity{
				Email:          "op@example.com",
				OrgMember:      true,
				ProviderUserID: "opuser",
				Provider:       "github",
			}, nil
		},
	}
	h, s, rec := newAuthHandlerWithProvider(t, prov)
	_, _ = s.AllowlistOperator(context.Background(), "op@example.com", store.OperatorRoleAdmin, "bootstrap")

	cbRec := runCallback(t, h, prov, "code-1")

	if cbRec.Code != http.StatusFound {
		t.Fatalf("want 302 redirect on success, got %d: %s", cbRec.Code, cbRec.Body.String())
	}
	// Session cookie present.
	var foundSession bool
	for _, c := range cbRec.Result().Cookies() {
		if c.Name == "packyard_session" && c.Value != "" {
			foundSession = true
			if c.SameSite != http.SameSiteStrictMode {
				t.Errorf("session cookie SameSite must be Strict (D15)")
			}
		}
	}
	if !foundSession {
		t.Error("callback should issue packyard_session cookie")
	}
	// login.success audit row.
	if len(rec.entries) != 1 || rec.entries[0].Action != "login.success" {
		t.Errorf("want one login.success audit row, got %+v", rec.entries)
	}
}

// TestAuth_Callback_CapturesFirstSeenProvider asserts the § 5.3 / D14
// opportunistic capture: a successful callback populates last_login_at,
// first_seen_provider, and the provider-specific identity column.
func TestAuth_Callback_CapturesFirstSeenProvider(t *testing.T) {
	prov := &fakeProvider{
		name: "github",
		exchangeFn: func(_ context.Context, _, _ string) (*auth.OAuthIdentity, error) {
			return &auth.OAuthIdentity{
				Email:          "op@example.com",
				OrgMember:      true,
				ProviderUserID: "opuser",
				Provider:       "github",
			}, nil
		},
	}
	h, s, _ := newAuthHandlerWithProvider(t, prov)
	op, _ := s.AllowlistOperator(context.Background(), "op@example.com", store.OperatorRoleAdmin, "bootstrap")
	if op.LastLoginAt != nil {
		t.Fatalf("precondition: allowlist row should have nil last_login_at")
	}

	cbRec := runCallback(t, h, prov, "code-1")
	if cbRec.Code != http.StatusFound {
		t.Fatalf("callback failed: %d %s", cbRec.Code, cbRec.Body.String())
	}

	after, _ := s.GetOperator(context.Background(), op.ID)
	if after.LastLoginAt == nil {
		t.Error("last_login_at should be set after successful callback")
	}
	if after.FirstSeenProvider != "github" {
		t.Errorf("first_seen_provider: want github, got %q", after.FirstSeenProvider)
	}
	if after.GithubUsername != "opuser" {
		t.Errorf("github_username: want opuser, got %q", after.GithubUsername)
	}
}

func TestAuth_Callback_NotOnAllowlist(t *testing.T) {
	prov := &fakeProvider{
		name: "github",
		exchangeFn: func(_ context.Context, _, _ string) (*auth.OAuthIdentity, error) {
			return &auth.OAuthIdentity{Email: "ghost@example.com", OrgMember: true, Provider: "github"}, nil
		},
	}
	h, _, rec := newAuthHandlerWithProvider(t, prov)

	cbRec := runCallback(t, h, prov, "code-1")
	if cbRec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", cbRec.Code)
	}
	var ae apiError
	json.NewDecoder(cbRec.Body).Decode(&ae)
	if ae.Code != "OPERATOR_NOT_ALLOWED" {
		t.Errorf("want OPERATOR_NOT_ALLOWED, got %q", ae.Code)
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != "login.failure" {
		t.Errorf("want one login.failure audit row, got %+v", rec.entries)
	}
}

func TestAuth_Callback_DisabledOperator(t *testing.T) {
	prov := &fakeProvider{
		name: "github",
		exchangeFn: func(_ context.Context, _, _ string) (*auth.OAuthIdentity, error) {
			return &auth.OAuthIdentity{Email: "disabled@example.com", OrgMember: true, Provider: "github"}, nil
		},
	}
	h, s, _ := newAuthHandlerWithProvider(t, prov)
	op, _ := s.AllowlistOperator(context.Background(), "disabled@example.com", store.OperatorRoleAdmin, "bootstrap")
	_ = s.DisableOperator(context.Background(), op.ID)

	cbRec := runCallback(t, h, prov, "code-1")
	if cbRec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", cbRec.Code)
	}
	var ae apiError
	json.NewDecoder(cbRec.Body).Decode(&ae)
	if ae.Code != "OPERATOR_DISABLED" {
		t.Errorf("want OPERATOR_DISABLED, got %q", ae.Code)
	}
}

func TestAuth_Callback_UnverifiedEmail(t *testing.T) {
	prov := &fakeProvider{
		name: "github",
		exchangeFn: func(_ context.Context, _, _ string) (*auth.OAuthIdentity, error) {
			return nil, auth.ErrUnverifiedEmail
		},
	}
	h, _, _ := newAuthHandlerWithProvider(t, prov)

	cbRec := runCallback(t, h, prov, "code-1")
	if cbRec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", cbRec.Code)
	}
	var ae apiError
	json.NewDecoder(cbRec.Body).Decode(&ae)
	if ae.Code != "EMAIL_NOT_VERIFIED" {
		t.Errorf("want EMAIL_NOT_VERIFIED, got %q", ae.Code)
	}
}

func TestAuth_Callback_NotOrgMember(t *testing.T) {
	prov := &fakeProvider{
		name: "github",
		exchangeFn: func(_ context.Context, _, _ string) (*auth.OAuthIdentity, error) {
			return nil, auth.ErrNotOrgMember
		},
	}
	h, _, _ := newAuthHandlerWithProvider(t, prov)

	cbRec := runCallback(t, h, prov, "code-1")
	if cbRec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", cbRec.Code)
	}
	var ae apiError
	json.NewDecoder(cbRec.Body).Decode(&ae)
	if ae.Code != "ORG_MEMBERSHIP_REQUIRED" {
		t.Errorf("want ORG_MEMBERSHIP_REQUIRED, got %q", ae.Code)
	}
}

func TestAuth_Callback_StateMismatch(t *testing.T) {
	prov := &fakeProvider{
		name: "github",
		exchangeFn: func(_ context.Context, _, _ string) (*auth.OAuthIdentity, error) {
			t.Fatal("exchange should not run on state mismatch")
			return nil, nil
		},
	}
	h, _, _ := newAuthHandlerWithProvider(t, prov)

	// Login to get a valid handle cookie.
	loginReq := chiProviderReq(httptest.NewRequest(http.MethodGet, "/api/v1/auth/login/github", nil), "github")
	loginRec := httptest.NewRecorder()
	h.Login(loginRec, loginReq)

	// Callback with a deliberately wrong state.
	cbReq := chiProviderReq(
		httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback/github?state=wrong&code=c", nil),
		"github",
	)
	for _, c := range loginRec.Result().Cookies() {
		cbReq.AddCookie(c)
	}
	cbRec := httptest.NewRecorder()
	h.Callback(cbRec, cbReq)
	if cbRec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", cbRec.Code)
	}
	var ae apiError
	json.NewDecoder(cbRec.Body).Decode(&ae)
	if ae.Code != "INVALID_OAUTH_STATE" {
		t.Errorf("want INVALID_OAUTH_STATE, got %q", ae.Code)
	}
}

func TestAuth_Callback_MissingHandleCookie(t *testing.T) {
	prov := &fakeProvider{name: "github"}
	h, _, _ := newAuthHandlerWithProvider(t, prov)

	cbReq := chiProviderReq(
		httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback/github?state=x&code=y", nil),
		"github",
	)
	cbRec := httptest.NewRecorder()
	h.Callback(cbRec, cbReq)
	if cbRec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", cbRec.Code)
	}
}

// TestAuth_Callback_FailurePathsClearHandleCookie verifies the patched
// defer-ordering fix: every failure exit MUST clear the handle cookie
// (previously the deferred clear was dropped because writeError had already
// committed the response headers).
func TestAuth_Callback_FailurePathsClearHandleCookie(t *testing.T) {
	prov := &fakeProvider{
		name: "github",
		exchangeFn: func(_ context.Context, _, _ string) (*auth.OAuthIdentity, error) {
			return nil, auth.ErrUnverifiedEmail
		},
	}
	h, _, _ := newAuthHandlerWithProvider(t, prov)
	cbRec := runCallback(t, h, prov, "code-1")
	if cbRec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", cbRec.Code)
	}
	var cleared bool
	for _, c := range cbRec.Result().Cookies() {
		if c.Name == "packyard_oauth_handle" && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("failure path must clear the handle cookie (Set-Cookie with MaxAge<0)")
	}
}

// TestAuth_Callback_CreateSessionFailureAudits verifies the CreateSession
// 500 path now writes a login.failure audit row (was missing entirely
// before the chunk-B review patch).
func TestAuth_Callback_CreateSessionFailureAudits(t *testing.T) {
	prov := &fakeProvider{
		name: "github",
		exchangeFn: func(_ context.Context, _, _ string) (*auth.OAuthIdentity, error) {
			return &auth.OAuthIdentity{Email: "boom@example.com", OrgMember: true, Provider: "github"}, nil
		},
	}
	h, s, rec := newAuthHandlerWithProvider(t, prov)
	_, _ = s.AllowlistOperator(context.Background(), "boom@example.com", store.OperatorRoleAdmin, "bootstrap")

	// Swap in a session store that always fails to create.
	h.Sessions = failingCreateSessionStore{base: s}

	cbRec := runCallback(t, h, prov, "code-1")
	if cbRec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d: %s", cbRec.Code, cbRec.Body.String())
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != "login.failure" {
		t.Errorf("expected one login.failure audit row, got %+v", rec.entries)
	}
}

// failingCreateSessionStore satisfies store.SessionStore but rejects every
// CreateSession with a fake error.
type failingCreateSessionStore struct {
	base store.SessionStore
}

func (failingCreateSessionStore) CreateSession(context.Context, string, string, string) (*store.Session, error) {
	return nil, errors.New("disk full")
}
func (f failingCreateSessionStore) GetSession(ctx context.Context, id string) (*store.Session, error) {
	return f.base.GetSession(ctx, id)
}
func (f failingCreateSessionStore) TouchSession(ctx context.Context, id string, now time.Time) error {
	return f.base.TouchSession(ctx, id, now)
}
func (f failingCreateSessionStore) DeleteSession(ctx context.Context, id string) error {
	return f.base.DeleteSession(ctx, id)
}
func (f failingCreateSessionStore) DeleteOperatorSessions(ctx context.Context, operatorID string) error {
	return f.base.DeleteOperatorSessions(ctx, operatorID)
}
