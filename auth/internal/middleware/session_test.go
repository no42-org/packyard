/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/no42-org/packyard-auth/internal/audit"
	"github.com/no42-org/packyard-auth/internal/auth"
	"github.com/no42-org/packyard-auth/internal/store"
)

func newSessionTestStore(t *testing.T) (*store.SQLiteStore, *store.Operator, *store.Session) {
	t.Helper()
	s, err := store.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()
	op, err := s.AllowlistOperator(ctx, "op@example.com", store.OperatorRoleAdmin, "bootstrap")
	if err != nil {
		t.Fatalf("AllowlistOperator: %v", err)
	}
	sess, err := s.CreateSession(ctx, op.ID, "127.0.0.1", "go-test")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return s, op, sess
}

func runWithSession(mw func(http.Handler) http.Handler, sessionID string) *httptest.ResponseRecorder {
	var capturedOperator auth.Operator
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedOperator, _ = auth.OperatorFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(capturedOperator.ID))
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/keys", nil)
	if sessionID != "" {
		req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessionID})
	}
	rec := httptest.NewRecorder()
	mw(inner).ServeHTTP(rec, req)
	return rec
}

func TestRequireSession_ValidCookieAllows(t *testing.T) {
	s, op, sess := newSessionTestStore(t)
	mw := RequireSession(SessionConfig{
		Sessions:  s,
		Operators: s,
		Logger:    slog.Default(),
	})
	rec := runWithSession(mw, sess.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != op.ID {
		t.Errorf("operator id propagated wrong: want %q, got %q", op.ID, rec.Body.String())
	}
}

func TestRequireSession_MissingCookieRejects(t *testing.T) {
	s, _, _ := newSessionTestStore(t)
	mw := RequireSession(SessionConfig{
		Sessions:  s,
		Operators: s,
		Logger:    slog.Default(),
	})
	rec := runWithSession(mw, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

// (Dev-mode bypass was removed per spec D5 — no environment-flag override.)

func TestRequireSession_UnknownCookieRejected(t *testing.T) {
	s, _, _ := newSessionTestStore(t)
	mw := RequireSession(SessionConfig{
		Sessions:  s,
		Operators: s,
		Logger:    slog.Default(),
	})
	rec := runWithSession(mw, "totally-bogus-session-id")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 for unknown session, got %d", rec.Code)
	}
}

func TestRequireSession_AbsoluteExpiryRejected(t *testing.T) {
	s, _, sess := newSessionTestStore(t)
	// Inject a fake clock pointing past expires_at.
	mw := RequireSession(SessionConfig{
		Sessions:  s,
		Operators: s,
		Logger:    slog.Default(),
		Now:       func() time.Time { return sess.ExpiresAt.Add(time.Minute) },
	})
	rec := runWithSession(mw, sess.ID)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
	// Session should be deleted on expiry.
	_, err := s.GetSession(context.Background(), sess.ID)
	if err == nil {
		t.Errorf("expected expired session to be deleted; still present")
	}
}

func TestRequireSession_IdleExpiryRejected(t *testing.T) {
	s, _, sess := newSessionTestStore(t)
	mw := RequireSession(SessionConfig{
		Sessions:  s,
		Operators: s,
		Logger:    slog.Default(),
		Now:       func() time.Time { return sess.LastSeenAt.Add(store.SessionIdleLifetime + time.Minute) },
	})
	rec := runWithSession(mw, sess.ID)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 on idle expiry, got %d", rec.Code)
	}
}

func TestRequireSession_DisabledOperatorRejected(t *testing.T) {
	s, op, sess := newSessionTestStore(t)
	if err := s.DisableOperator(context.Background(), op.ID); err != nil {
		t.Fatalf("disable operator: %v", err)
	}
	mw := RequireSession(SessionConfig{
		Sessions:  s,
		Operators: s,
		Logger:    slog.Default(),
	})
	rec := runWithSession(mw, sess.ID)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 for disabled operator, got %d", rec.Code)
	}
}

func TestRequireSession_TouchesLastSeen(t *testing.T) {
	s, _, sess := newSessionTestStore(t)
	wantNow := sess.CreatedAt.Add(time.Hour)
	mw := RequireSession(SessionConfig{
		Sessions:  s,
		Operators: s,
		Logger:    slog.Default(),
		Now:       func() time.Time { return wantNow },
	})
	_ = runWithSession(mw, sess.ID)
	got, _ := s.GetSession(context.Background(), sess.ID)
	want := wantNow.UTC().Truncate(time.Second)
	if !got.LastSeenAt.Equal(want) {
		t.Errorf("last_seen_at: want %v, got %v", want, got.LastSeenAt)
	}
}

// TestRequireSession_MissingCookieEmitsUnauthorized verifies the patched
// error-code distinction: no cookie → UNAUTHORIZED (not SESSION_EXPIRED).
func TestRequireSession_MissingCookieEmitsUnauthorized(t *testing.T) {
	s, _, _ := newSessionTestStore(t)
	mw := RequireSession(SessionConfig{Sessions: s, Operators: s, Logger: slog.Default()})
	rec := runWithSession(mw, "")
	var body struct{ Code string }
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if body.Code != "UNAUTHORIZED" {
		t.Errorf("want code UNAUTHORIZED, got %q", body.Code)
	}
}

// TestRequireSession_UnknownCookieEmitsUnauthorized — same: unknown session
// is "no valid session" not "session expired".
func TestRequireSession_UnknownCookieEmitsUnauthorized(t *testing.T) {
	s, _, _ := newSessionTestStore(t)
	mw := RequireSession(SessionConfig{Sessions: s, Operators: s, Logger: slog.Default()})
	rec := runWithSession(mw, "totally-bogus")
	var body struct{ Code string }
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if body.Code != "UNAUTHORIZED" {
		t.Errorf("want code UNAUTHORIZED, got %q", body.Code)
	}
}

// TestRequireSession_ExpiryEmitsSessionExpired — only the timeout branch
// uses SESSION_EXPIRED.
func TestRequireSession_ExpiryEmitsSessionExpired(t *testing.T) {
	s, _, sess := newSessionTestStore(t)
	mw := RequireSession(SessionConfig{
		Sessions:  s,
		Operators: s,
		Logger:    slog.Default(),
		Now:       func() time.Time { return sess.ExpiresAt.Add(time.Minute) },
	})
	rec := runWithSession(mw, sess.ID)
	var body struct{ Code string }
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if body.Code != "SESSION_EXPIRED" {
		t.Errorf("want code SESSION_EXPIRED on timeout, got %q", body.Code)
	}
}

// TestRequireSession_TransientStoreErrorKeepsCookie — a non-NotFound store
// error must NOT clear the cookie or log everyone out.
func TestRequireSession_TransientStoreErrorKeepsCookie(t *testing.T) {
	mw := RequireSession(SessionConfig{
		Sessions:  &failingSessionStore{},
		Operators: nil, // never reached because Sessions fails first
		Logger:    slog.Default(),
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/keys", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "abc"})
	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("handler should not run on transient store error")
	})).ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", rec.Code)
	}
	// No Set-Cookie that clears the session.
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookieName && c.MaxAge < 0 {
			t.Errorf("transient store error should not clear cookie")
		}
	}
}

// TestRequireSession_TouchSkippedOnRoleDeny — a 403 from the wrapped handler
// must NOT slide last_seen_at (D17: "every accepted request").
func TestRequireSession_TouchSkippedOnRoleDeny(t *testing.T) {
	s, _, sess := newSessionTestStore(t)
	originalLastSeen := sess.LastSeenAt

	mw := RequireSession(SessionConfig{
		Sessions:  s,
		Operators: s,
		Logger:    slog.Default(),
		Now:       func() time.Time { return originalLastSeen.Add(2 * time.Hour) },
	})

	// Inner handler simulates a 403 ROLE_DENIED.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/keys", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sess.ID})
	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})).ServeHTTP(rec, req)

	got, _ := s.GetSession(context.Background(), sess.ID)
	if !got.LastSeenAt.Equal(originalLastSeen.UTC().Truncate(time.Second)) {
		t.Errorf("403 should not slide last_seen_at; was %v, got %v",
			originalLastSeen.UTC().Truncate(time.Second), got.LastSeenAt)
	}
}

// failingSessionStore returns a non-NotFound error from GetSession so the
// transient-error path of RequireSession can be exercised.
type failingSessionStore struct{}

func (failingSessionStore) CreateSession(context.Context, string, string, string) (*store.Session, error) {
	panic("unexpected")
}
func (failingSessionStore) GetSession(context.Context, string) (*store.Session, error) {
	return nil, errors.New("database connection refused")
}
func (failingSessionStore) TouchSession(context.Context, string, time.Time) error {
	panic("unexpected")
}
func (failingSessionStore) DeleteSession(context.Context, string) error { panic("unexpected") }
func (failingSessionStore) DeleteOperatorSessions(context.Context, string) error {
	panic("unexpected")
}

// ─── Role middleware ────────────────────────────────────────────────────────

func runWithRole(role auth.Role, method, path string) *httptest.ResponseRecorder {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(method, path, nil)
	ctx := auth.WithOperator(req.Context(), auth.Operator{ID: "op", Role: role})
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	RequireRole(RoleConfig{})(inner).ServeHTTP(rec, req)
	return rec
}

func TestRequireRole_AdminPostAllowed(t *testing.T) {
	rec := runWithRole(auth.RoleAdmin, http.MethodPost, "/api/v1/accounts")
	if rec.Code != http.StatusOK {
		t.Errorf("admin POST: want 200, got %d", rec.Code)
	}
}

// roleAuditRecorder captures audit entries emitted by RequireRole.
type roleAuditRecorder struct{ entries []audit.Entry }

func (r *roleAuditRecorder) Write(_ context.Context, e audit.Entry) {
	r.entries = append(r.entries, e)
}

// TestRequireRole_ReadonlyDeniedEmitsAuditRow verifies the § 6 review fix:
// every ROLE_DENIED 403 must persist an `auth.role_denied` audit row per
// operator-authn spec § "Audit log coverage".
func TestRequireRole_ReadonlyDeniedEmitsAuditRow(t *testing.T) {
	rec := &roleAuditRecorder{}
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("inner should not run on denial")
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts", nil)
	req = req.WithContext(auth.WithOperator(req.Context(),
		auth.Operator{ID: "ro-op", Role: auth.RoleReadonly}))
	w := httptest.NewRecorder()
	RequireRole(RoleConfig{Auditor: rec})(inner).ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", w.Code)
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != "auth.role_denied" {
		t.Errorf("want one auth.role_denied audit row, got %+v", rec.entries)
	}
	if rec.entries[0].OperatorID != "ro-op" {
		t.Errorf("operator_id should be ro-op, got %q", rec.entries[0].OperatorID)
	}
}

func TestRequireRole_ReadonlyGetAllowed(t *testing.T) {
	rec := runWithRole(auth.RoleReadonly, http.MethodGet, "/api/v1/accounts")
	if rec.Code != http.StatusOK {
		t.Errorf("readonly GET: want 200, got %d", rec.Code)
	}
}

func TestRequireRole_ReadonlyPostDenied(t *testing.T) {
	rec := runWithRole(auth.RoleReadonly, http.MethodPost, "/api/v1/accounts")
	if rec.Code != http.StatusForbidden {
		t.Errorf("readonly POST: want 403, got %d", rec.Code)
	}
}

func TestRequireRole_ReadonlyLogoutAllowed(t *testing.T) {
	rec := runWithRole(auth.RoleReadonly, http.MethodPost, "/api/v1/auth/logout")
	if rec.Code != http.StatusOK {
		t.Errorf("readonly logout: want 200 (D24 exemption), got %d", rec.Code)
	}
}

// TestRequireRole_ReadonlyLogoutTrailingSlashAllowed verifies the
// path-normalisation patch — a trailing slash on the logout URL must not
// trip the role gate.
func TestRequireRole_ReadonlyLogoutTrailingSlashAllowed(t *testing.T) {
	rec := runWithRole(auth.RoleReadonly, http.MethodPost, "/api/v1/auth/logout/")
	if rec.Code != http.StatusOK {
		t.Errorf("readonly logout w/ trailing slash: want 200, got %d", rec.Code)
	}
}

// TestRequireRole_NoOperatorRejected verifies the fail-closed default for
// missing operator context (D5 + the OperatorFromContext (Operator, bool)
// signature change).
func TestRequireRole_NoOperatorRejected(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not run without an operator in context")
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts", nil)
	rec := httptest.NewRecorder()
	RequireRole(RoleConfig{})(inner).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no operator: want 401, got %d", rec.Code)
	}
}
