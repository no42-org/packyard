package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func newTestOperatorAndSession(t *testing.T, s *SQLiteStore) (*Operator, *Session) {
	t.Helper()
	ctx := context.Background()
	op, err := s.AllowlistOperator(ctx, "op@example.com", OperatorRoleAdmin, "bootstrap")
	if err != nil {
		t.Fatalf("AllowlistOperator: %v", err)
	}
	sess, err := s.CreateSession(ctx, op.ID, "127.0.0.1", "go-test")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return op, sess
}

func TestSessionStore_CreateAndGet(t *testing.T) {
	s := newTestStore(t)
	_, sess := newTestOperatorAndSession(t, s)
	if len(sess.ID) != 64 {
		t.Errorf("session id length: want 64 hex chars, got %d", len(sess.ID))
	}
	got, err := s.GetSession(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.OperatorID != sess.OperatorID {
		t.Errorf("operator_id round-trip mismatch")
	}
	if got.ExpiresAt.Sub(got.CreatedAt) != SessionAbsoluteLifetime {
		t.Errorf("expires_at: want createdAt + %v, got delta %v",
			SessionAbsoluteLifetime, got.ExpiresAt.Sub(got.CreatedAt))
	}
}

func TestSessionStore_GetUnknownReturnsNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetSession(context.Background(), "nope")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("want ErrSessionNotFound, got %v", err)
	}
}

func TestSessionStore_Touch(t *testing.T) {
	s := newTestStore(t)
	_, sess := newTestOperatorAndSession(t, s)
	later := sess.CreatedAt.Add(2 * time.Hour)
	if err := s.TouchSession(context.Background(), sess.ID, later); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}
	got, _ := s.GetSession(context.Background(), sess.ID)
	// Store rounds to second precision via RFC3339 format.
	want := later.UTC().Truncate(time.Second)
	if !got.LastSeenAt.Equal(want) {
		t.Errorf("last_seen_at: want %v, got %v", want, got.LastSeenAt)
	}
}

func TestSessionStore_DeleteOnLogout(t *testing.T) {
	s := newTestStore(t)
	_, sess := newTestOperatorAndSession(t, s)
	if err := s.DeleteSession(context.Background(), sess.ID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	_, err := s.GetSession(context.Background(), sess.ID)
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("want ErrSessionNotFound after delete, got %v", err)
	}
	// Idempotent.
	if err := s.DeleteSession(context.Background(), sess.ID); err != nil {
		t.Errorf("delete on missing session should be idempotent, got %v", err)
	}
}

func TestSessionStore_DeleteOperatorSessions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	op, _ := newTestOperatorAndSession(t, s)
	// Add a second session for the same operator.
	if _, err := s.CreateSession(ctx, op.ID, "10.0.0.1", "another"); err != nil {
		t.Fatalf("CreateSession #2: %v", err)
	}
	if err := s.DeleteOperatorSessions(ctx, op.ID); err != nil {
		t.Fatalf("DeleteOperatorSessions: %v", err)
	}
	var remaining int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sessions WHERE operator_id = ?`, op.ID).Scan(&remaining); err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 0 {
		t.Errorf("want 0 remaining sessions, got %d", remaining)
	}
}

// ─── Operator store + bootstrap ─────────────────────────────────────────────

func TestOperatorStore_BootstrapFromEnv_Empty(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	op, err := s.BootstrapOperatorFromEnv(ctx, "admin@example.com")
	if err != nil {
		t.Fatalf("BootstrapOperatorFromEnv: %v", err)
	}
	if op == nil {
		t.Fatal("expected operator to be created, got nil")
	}
	if op.Email != "admin@example.com" {
		t.Errorf("email mismatch: %q", op.Email)
	}
	if op.Role != OperatorRoleAdmin {
		t.Errorf("role: want admin, got %q", op.Role)
	}
}

func TestOperatorStore_BootstrapFromEnv_InertAfterFirstOperator(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.BootstrapOperatorFromEnv(ctx, "first@example.com"); err != nil {
		t.Fatalf("first bootstrap: %v", err)
	}
	// Second call must be a no-op even with a different email.
	op, err := s.BootstrapOperatorFromEnv(ctx, "second@example.com")
	if err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	if op != nil {
		t.Errorf("second bootstrap should be inert; got operator %+v", op)
	}
	// Verify only one operator exists.
	n, _ := s.OperatorCount(ctx)
	if n != 1 {
		t.Errorf("operator count: want 1, got %d", n)
	}
}

func TestOperatorStore_BootstrapFromEnv_EmptyEmailIsNoop(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	op, err := s.BootstrapOperatorFromEnv(ctx, "")
	if err != nil {
		t.Fatalf("empty email bootstrap: %v", err)
	}
	if op != nil {
		t.Errorf("empty email should produce no operator; got %+v", op)
	}
	n, _ := s.OperatorCount(ctx)
	if n != 0 {
		t.Errorf("operator count after empty bootstrap: want 0, got %d", n)
	}
}

func TestOperatorStore_GetByEmailCanonicalised(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.AllowlistOperator(ctx, "Mixed@Case.Example", OperatorRoleReadonly, "bootstrap"); err != nil {
		t.Fatalf("Allowlist: %v", err)
	}
	op, err := s.GetOperatorByEmail(ctx, "MIXED@CASE.EXAMPLE")
	if err != nil {
		t.Fatalf("GetOperatorByEmail: %v", err)
	}
	if op.Email != "mixed@case.example" {
		t.Errorf("canonical email: got %q", op.Email)
	}
}
