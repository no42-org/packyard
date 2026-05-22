package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/no42-org/packyard-auth/internal/auth"
	"github.com/no42-org/packyard-auth/internal/store"
)

// SessionCookieName is the name of the cookie carrying the opaque session id.
const SessionCookieName = "packyard_session"

// SessionConfig wires the session middleware to its stores.
type SessionConfig struct {
	Sessions  store.SessionStore
	Operators store.OperatorStore
	Logger    *slog.Logger
	// Now is injected for tests; defaults to time.Now when nil.
	Now func() time.Time
}

// sessionIDLogPrefix returns the first 8 hex chars of the SHA-256 of the
// session id, used in log lines that need to correlate without leaking the
// raw bearer credential.
func sessionIDLogPrefix(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:4])
}

// errBody mirrors the handler-layer apiError shape so the middleware can emit
// the same Code+Message pattern without depending on the handler package.
type errBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errBody{Code: code, Message: message})
}

// RequireSession returns a middleware that:
//   - Reads the SessionCookieName cookie
//   - Looks up the session row and the owning operator
//   - Enforces idle 8h + absolute 24h timeouts (D17)
//   - Rejects disabled operators
//   - Touches last_seen_at AFTER the wrapped handler returns success (status < 400),
//     so a role-denied request does not slide the idle window
//   - Injects auth.Operator into the request context
//
// Error codes follow the spec table:
//   - UNAUTHORIZED   — no cookie present, or the cookie value does not match
//                      any session row
//   - SESSION_EXPIRED — the session row exists but exceeded idle or absolute
//                       lifetime, or the owning operator is disabled
//   - 500 with no cookie change — transient store error (don't log everyone out)
//
// Cookies are cleared only on definitively-invalid sessions, not on
// transient backend failures.
func RequireSession(cfg SessionConfig) func(http.Handler) http.Handler {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(SessionCookieName)
			if err != nil || cookie.Value == "" {
				clearSessionCookie(w)
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED",
					"no session cookie; sign in to obtain one")
				return
			}

			sess, err := cfg.Sessions.GetSession(r.Context(), cookie.Value)
			if err != nil {
				if errors.Is(err, store.ErrSessionNotFound) {
					clearSessionCookie(w)
					writeError(w, http.StatusUnauthorized, "UNAUTHORIZED",
						"session not found or has been revoked")
					return
				}
				// Transient backend error — keep the cookie so the operator
				// isn't forcibly logged out by a flaky DB. Return 500.
				cfg.Logger.Error("session lookup failed",
					slog.String("session_id_prefix", sessionIDLogPrefix(cookie.Value)),
					slog.String("error", err.Error()))
				writeError(w, http.StatusInternalServerError, "SESSION_LOOKUP_FAILED",
					"failed to resolve session; try again")
				return
			}

			nowTime := now().UTC()
			if nowTime.After(sess.ExpiresAt) || nowTime.Sub(sess.LastSeenAt) > store.SessionIdleLifetime {
				// Soft-delete the expired session row so future lookups
				// short-circuit. Errors here are non-fatal — we still
				// return the 401.
				_ = cfg.Sessions.DeleteSession(r.Context(), sess.ID)
				clearSessionCookie(w)
				writeError(w, http.StatusUnauthorized, "SESSION_EXPIRED",
					"session has expired; sign in again")
				return
			}

			op, err := cfg.Operators.GetOperator(r.Context(), sess.OperatorID)
			if err != nil {
				if errors.Is(err, store.ErrOperatorNotFound) {
					_ = cfg.Sessions.DeleteSession(r.Context(), sess.ID)
					clearSessionCookie(w)
					writeError(w, http.StatusUnauthorized, "SESSION_EXPIRED",
						"operator account is not available")
					return
				}
				cfg.Logger.Error("operator lookup failed",
					slog.String("session_id_prefix", sessionIDLogPrefix(sess.ID)),
					slog.String("error", err.Error()))
				writeError(w, http.StatusInternalServerError, "OPERATOR_LOOKUP_FAILED",
					"failed to resolve operator; try again")
				return
			}
			if op.Status != store.OperatorStatusActive {
				_ = cfg.Sessions.DeleteSession(r.Context(), sess.ID)
				clearSessionCookie(w)
				writeError(w, http.StatusUnauthorized, "SESSION_EXPIRED",
					"the operator account is disabled")
				return
			}

			ctx := auth.WithOperator(r.Context(), auth.Operator{
				ID:    op.ID,
				Email: op.Email,
				Role:  auth.Role(op.Role),
			})

			// Capture the downstream response status so we only touch
			// last_seen_at on accepted requests (D17 wording).
			tw := &touchTrackingWriter{ResponseWriter: w}
			next.ServeHTTP(tw, r.WithContext(ctx))

			if tw.status >= 200 && tw.status < 400 {
				if err := cfg.Sessions.TouchSession(r.Context(), sess.ID, nowTime); err != nil {
					cfg.Logger.Warn("touch session failed",
						slog.String("session_id_prefix", sessionIDLogPrefix(sess.ID)),
						slog.String("error", err.Error()))
				}
			}
		})
	}
}

// touchTrackingWriter records the response status so the session middleware
// can decide whether the request was "accepted" before touching last_seen_at.
// Defaults to 200 when no explicit WriteHeader is called (net/http convention).
type touchTrackingWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (t *touchTrackingWriter) WriteHeader(code int) {
	if !t.wroteHeader {
		t.status = code
		t.wroteHeader = true
	}
	t.ResponseWriter.WriteHeader(code)
}

func (t *touchTrackingWriter) Write(b []byte) (int, error) {
	if !t.wroteHeader {
		t.status = http.StatusOK
		t.wroteHeader = true
	}
	return t.ResponseWriter.Write(b)
}

// clearSessionCookie sets the cookie to an expired empty value so the browser
// stops sending the rejected session id.
func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

// IssueSessionCookie writes the session cookie with the D15-required attributes.
// Called by the OAuth callback and the dev-only login endpoints.
func IssueSessionCookie(w http.ResponseWriter, sessionID string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    sessionID,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

