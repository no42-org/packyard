package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/no42-org/packyard-auth/internal/audit"
	"github.com/no42-org/packyard-auth/internal/auth"
	"github.com/no42-org/packyard-auth/internal/middleware"
	"github.com/no42-org/packyard-auth/internal/store"
)

// oauthHandleCookieName carries the opaque handle that the login handler
// hands to the browser and the callback handler consumes. Path scoped to /
// (default) so the browser sends it back on the callback no matter how the
// IdP redirect is structured. SameSite=Lax (not Strict) is required: the
// callback request comes from the IdP origin, which is cross-site; Strict
// would block the cookie. Short MaxAge bounds the leak surface.
const oauthHandleCookieName = "packyard_oauth_handle"

// AuthHandler serves the /api/v1/auth/* endpoints.
type AuthHandler struct {
	Sessions  store.SessionStore
	Operators store.OperatorStore
	Providers map[string]auth.OAuthProvider
	State     auth.StateStore
	Auditor   audit.Auditor
	Logger    *slog.Logger
	// LoginSuccessRedirect is the URL the browser is sent to after a
	// successful login. Defaults to "/admin/" when zero.
	LoginSuccessRedirect string
}

// NewAuthHandler returns a handler. Providers may be nil/empty if only the
// logout endpoint is exercised (chunk A tests do this).
func NewAuthHandler(sessions store.SessionStore, auditor audit.Auditor, logger *slog.Logger) *AuthHandler {
	if auditor == nil {
		auditor = audit.NoopAuditor{Logger: logger}
	}
	return &AuthHandler{
		Sessions: sessions,
		Auditor:  auditor,
		Logger:   logger,
	}
}

// ─── Login ──────────────────────────────────────────────────────────────────

// Login handles GET /api/v1/auth/login/{provider}. Generates state + PKCE,
// stores them under an opaque handle in a transient cookie, and redirects
// the browser to the provider's authorize URL.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	providerName := chi.URLParam(r, "provider")
	provider, ok := h.Providers[providerName]
	if !ok {
		redirectLoginError(w, r, "UNKNOWN_PROVIDER")
		return
	}

	state, err := auth.RandomHex(32)
	if err != nil {
		h.Logger.Error("generate state failed", slog.String("error", err.Error()))
		redirectLoginError(w, r, "LOGIN_INIT_FAILED")
		return
	}
	verifier, err := auth.RandomHex(32)
	if err != nil {
		h.Logger.Error("generate verifier failed", slog.String("error", err.Error()))
		redirectLoginError(w, r, "LOGIN_INIT_FAILED")
		return
	}
	handle, err := auth.RandomHex(16)
	if err != nil {
		h.Logger.Error("generate handle failed", slog.String("error", err.Error()))
		redirectLoginError(w, r, "LOGIN_INIT_FAILED")
		return
	}

	h.State.Put(handle, auth.StateEntry{
		State:        state,
		CodeVerifier: verifier,
		Provider:     providerName,
	})

	http.SetCookie(w, &http.Cookie{
		Name:     oauthHandleCookieName,
		Value:    handle,
		Path:     "/",
		MaxAge:   int(auth.StateTTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, provider.AuthorizeURL(state, auth.PKCECodeChallenge(verifier)), http.StatusFound)
}

// ─── Callback ───────────────────────────────────────────────────────────────

// Callback handles GET /api/v1/auth/callback/{provider}. Verifies state,
// exchanges the code, matches against the operators table, creates a session,
// and redirects to LoginSuccessRedirect.
//
// Cookie hygiene: every exit path clears the handle cookie BEFORE writing
// the response header (`http.SetCookie` after `WriteHeader` is silently
// dropped). The helpers `respondError` and `respondRedirect` enforce this
// ordering — do not bypass them.
func (h *AuthHandler) Callback(w http.ResponseWriter, r *http.Request) {
	providerName := chi.URLParam(r, "provider")
	provider, ok := h.Providers[providerName]
	if !ok {
		// A stale handle cookie from a prior aborted flow could be present;
		// clear both the cookie AND consume the matching state entry so the
		// two paths stay symmetric. Otherwise the state entry would sit in
		// the memstore for the full 15-minute TTL while the cookie is gone —
		// an asymmetry an attacker could exploit by reinjecting the handle.
		if c, err := r.Cookie(oauthHandleCookieName); err == nil && c.Value != "" {
			_, _ = h.State.Consume(c.Value)
		}
		clearOAuthHandleCookie(w)
		redirectLoginError(w, r, "UNKNOWN_PROVIDER")
		return
	}

	queryState := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if queryState == "" || code == "" {
		h.failCallback(w, r, "", "", http.StatusBadRequest, "INVALID_OAUTH_STATE",
			"state and code query parameters are required",
			"missing_state_or_code")
		return
	}

	handleCookie, err := r.Cookie(oauthHandleCookieName)
	if err != nil || handleCookie.Value == "" {
		h.failCallback(w, r, "", "", http.StatusBadRequest, "INVALID_OAUTH_STATE",
			"no oauth handle cookie present",
			"missing_handle_cookie")
		return
	}

	entry, err := h.State.Consume(handleCookie.Value)
	if err != nil {
		h.failCallback(w, r, "", "", http.StatusBadRequest, "INVALID_OAUTH_STATE",
			"oauth handle expired or unknown",
			"state_not_found")
		return
	}
	if entry.State != queryState || entry.Provider != providerName {
		h.failCallback(w, r, "", "", http.StatusBadRequest, "INVALID_OAUTH_STATE",
			"state or provider mismatch",
			"state_mismatch")
		return
	}

	identity, err := provider.Exchange(r.Context(), code, entry.CodeVerifier)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrUnverifiedEmail), errors.Is(err, auth.ErrNoEmail):
			h.failCallback(w, r, "", providerName, http.StatusForbidden, "EMAIL_NOT_VERIFIED",
				"provider did not return a verified primary email",
				"unverified_email")
		case errors.Is(err, auth.ErrNotOrgMember):
			h.failCallback(w, r, "", providerName, http.StatusForbidden, "ORG_MEMBERSHIP_REQUIRED",
				"not a member of the allowed organisation/tenant",
				"not_org_member")
		default:
			h.Logger.Error("oauth exchange failed",
				slog.String("provider", providerName),
				slog.String("error", err.Error()))
			h.failCallback(w, r, "", providerName, http.StatusUnauthorized, "OAUTH_EXCHANGE_FAILED",
				"failed to exchange authorisation code",
				"exchange_failed")
		}
		return
	}

	// Defensive: providers are expected to map this to ErrNoEmail, but a
	// future provider impl might return an empty Email with no error.
	if identity.Email == "" {
		h.failCallback(w, r, "", providerName, http.StatusForbidden, "EMAIL_NOT_VERIFIED",
			"provider returned an empty email",
			"empty_email")
		return
	}

	op, err := h.Operators.GetOperatorByEmail(r.Context(), identity.Email)
	if err != nil {
		if errors.Is(err, store.ErrOperatorNotFound) {
			h.failCallback(w, r, identity.Email, providerName, http.StatusForbidden, "OPERATOR_NOT_ALLOWED",
				"the authenticated identity is not on the operator allowlist",
				"not_on_allowlist")
			return
		}
		h.Logger.Error("operator lookup failed", slog.String("error", err.Error()))
		h.failCallback(w, r, identity.Email, providerName, http.StatusInternalServerError, "OPERATOR_LOOKUP_FAILED",
			"failed to resolve operator", "lookup_failed")
		return
	}
	// Defensive switch so a future status value (e.g. 'locked') fails closed
	// instead of silently being accepted.
	switch op.Status {
	case store.OperatorStatusActive:
		// proceed
	case store.OperatorStatusDisabled:
		h.failCallback(w, r, identity.Email, providerName, http.StatusForbidden, "OPERATOR_DISABLED",
			"the operator account is disabled",
			"operator_disabled")
		return
	default:
		h.failCallback(w, r, identity.Email, providerName, http.StatusForbidden, "OPERATOR_DISABLED",
			"the operator account is not in an active state",
			"operator_status_"+string(op.Status))
		return
	}

	sess, err := h.Sessions.CreateSession(r.Context(), op.ID, clientIP(r), r.UserAgent())
	if err != nil {
		h.Logger.Error("create session failed", slog.String("error", err.Error()))
		h.failCallback(w, r, identity.Email, providerName, http.StatusInternalServerError,
			"SESSION_CREATE_FAILED", "failed to create session", "session_create_failed")
		return
	}

	// § 5.3 / D14: opportunistically capture last_login_at and the
	// provider-specific identity columns on first OAuth login from this
	// provider. Both are best-effort — a persistence failure shouldn't
	// block the login that just succeeded.
	if err := h.Operators.UpdateLastLogin(r.Context(), op.ID, sess.CreatedAt); err != nil {
		h.Logger.Warn("update last_login_at failed",
			slog.String("operator_id", op.ID),
			slog.String("error", err.Error()))
	}
	if identity.ProviderUserID == "" {
		// UpdateLoginProvider silently no-ops on empty user id — surface the
		// missing first_seen capture so it's observable in production rather
		// than buried as a silent column-stays-NULL outcome.
		h.Logger.Warn("oauth identity missing provider user id; skipping first_seen capture",
			slog.String("operator_id", op.ID),
			slog.String("provider", providerName))
	} else if err := h.Operators.UpdateLoginProvider(r.Context(), op.ID, providerName, identity.ProviderUserID); err != nil {
		h.Logger.Warn("update login provider failed",
			slog.String("operator_id", op.ID),
			slog.String("provider", providerName),
			slog.String("error", err.Error()))
	}

	// Clear handle BEFORE issuing session cookie + redirect (both write
	// headers). http.SetCookie after WriteHeader is silently dropped.
	clearOAuthHandleCookie(w)
	middleware.IssueSessionCookie(w, sess.ID, sess.ExpiresAt)

	audit.WriteFromRequest(r.Context(), h.Auditor, r, audit.Entry{
		OperatorID: op.ID,
		Action:     "login.success",
		TargetType: "session",
		TargetID:   sess.ID,
		Details: map[string]any{
			"provider":         providerName,
			"provider_user_id": identity.ProviderUserID,
			"email":            identity.Email,
		},
	})

	dest := h.LoginSuccessRedirect
	if dest == "" {
		dest = "/admin/"
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

// failCallback clears the transient handle cookie, redirects the browser to
// /admin/login?error=CODE, and records a `login.failure` audit row capturing
// the failure mode. operatorEmail and provider are best-effort; either may
// be empty when the failure occurred before the relevant value was
// determined. The `status` and `message` arguments are retained for audit
// context (logged) but no longer surface to the browser — the SPA's Login
// route reads `?error=CODE` and renders a friendlier message.
//
// Cookie hygiene: clearOAuthHandleCookie MUST run before http.Redirect so
// the Set-Cookie header lands in the response.
func (h *AuthHandler) failCallback(w http.ResponseWriter, r *http.Request,
	operatorEmail, providerName string,
	status int, code, message, reason string) {

	clearOAuthHandleCookie(w)
	redirectLoginError(w, r, code)

	details := map[string]any{
		"reason":      reason,
		"http_status": status,
		"message":     message,
	}
	if operatorEmail != "" {
		details["email"] = operatorEmail
	}
	if providerName != "" {
		details["provider"] = providerName
	}
	audit.WriteFromRequest(r.Context(), h.Auditor, r, audit.Entry{
		Action:     "login.failure",
		TargetType: "session",
		Details:    details,
	})
}

// redirectLoginError sends the browser back to the SPA login route with an
// error code in the query string. The SPA's `KNOWN_ERROR_MESSAGES` map
// renders a human-readable banner. Non-browser callers (curl, scripts) see
// a 302 with an empty body — still recoverable; for fully-headless callers
// the audit log is the authoritative record.
func redirectLoginError(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, "/admin/login?error="+code, http.StatusFound)
}

// clientIP returns the request's source IP, parsing the leftmost XFF token
// when present (Traefik convention) so audit + session rows record a clean
// IP literal instead of the full forwarding chain.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := indexByte(xff, ','); i >= 0 {
			return trimSpace(xff[:i])
		}
		return trimSpace(xff)
	}
	return r.RemoteAddr
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

// clearOAuthHandleCookie zeros the transient handle cookie.
func clearOAuthHandleCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     oauthHandleCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// ─── Whoami ─────────────────────────────────────────────────────────────────

// Whoami handles GET /api/v1/auth/whoami. Returns the current operator
// (resolved from the session by RequireSession). The SPA hits this once at
// mount to render the chrome (operator email, role-gated nav). Unauthenticated
// requests return 401 from the upstream middleware before reaching this
// handler — defensive check below covers the test/mis-wiring case.
func (h *AuthHandler) Whoami(w http.ResponseWriter, r *http.Request) {
	op, ok := auth.OperatorFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED",
			"no authenticated operator on the request")
		return
	}
	// Look up the full operator row so the SPA can render `status` and other
	// fields the context doesn't carry. Session middleware already validated
	// the operator exists + is active, so a failure here is a transient store
	// error — surface it as 500 so the SPA can back off, rather than returning
	// 200 with a partial shape that the client can't distinguish from intent.
	full, err := h.Operators.GetOperator(r.Context(), op.ID)
	if err != nil {
		h.Logger.Warn("whoami lookup failed", slog.String("operator_id", op.ID), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "SESSION_LOOKUP_FAILED",
			"failed to resolve operator for current session")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(full)
}

// ─── Logout ─────────────────────────────────────────────────────────────────

// Logout handles POST /api/v1/auth/logout. Idempotent: a request with no
// session cookie still returns 204 + clears the cookie. The role gate exempts
// this endpoint per D24 so readonly operators can sign out.
//
// Audit semantics: an audit row is written only when a real session was
// actually removed. A no-cookie request (or one whose session id didn't match
// a row) returns 204 without polluting the audit log with an unattributed
// "logout" entry.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	op, opOK := auth.OperatorFromContext(r.Context())

	var (
		sessionDeleted bool
		deleteErr      error
	)
	if c, err := r.Cookie(middleware.SessionCookieName); err == nil && c.Value != "" {
		if err := h.Sessions.DeleteSession(r.Context(), c.Value); err != nil {
			h.Logger.Warn("delete session on logout failed",
				slog.String("error", err.Error()))
			deleteErr = err
		} else {
			sessionDeleted = true
		}
	}

	// Clear the cookie regardless of whether we found one.
	http.SetCookie(w, &http.Cookie{
		Name:     middleware.SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})

	// Audit the logout intent whenever an authenticated operator was on the
	// request, regardless of whether DeleteSession actually removed a row.
	// Recording failure too means a flaky DB doesn't drop the audit trail of
	// an operator who tried to sign out. Gate on `opOK` so handler-test paths
	// that bypass the session middleware don't trigger a "missing operator"
	// audit warning.
	if opOK {
		outcome := "failure"
		if sessionDeleted {
			outcome = "success"
		}
		details := map[string]any{"outcome": outcome}
		if deleteErr != nil {
			details["error"] = deleteErr.Error()
		}
		audit.WriteFromRequest(r.Context(), h.Auditor, r, audit.Entry{
			OperatorID: op.ID,
			Action:     "logout",
			TargetType: "session",
			Details:    details,
		})
	}

	w.WriteHeader(http.StatusNoContent)
}

