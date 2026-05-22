package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// Session lifetimes per D17 of change 2026-05-21-admin-ui-account-lifecycle.
const (
	SessionAbsoluteLifetime = 24 * time.Hour
	SessionIdleLifetime     = 8 * time.Hour
)

// generateSessionID returns 32 random bytes hex-encoded (64 chars) per D16.
func generateSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *SQLiteStore) CreateSession(ctx context.Context, operatorID, ip, userAgent string) (*Session, error) {
	id, err := generateSessionID()
	if err != nil {
		return nil, fmt.Errorf("generate session id: %w", err)
	}
	now := time.Now().UTC()
	expires := now.Add(SessionAbsoluteLifetime)

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO sessions (id, operator_id, created_at, last_seen_at, expires_at, ip, user_agent)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, operatorID,
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
		expires.Format(time.RFC3339),
		ip, userAgent,
	)
	if err != nil {
		return nil, classify("create session", err, constraintErrorMap{
			ForeignErr: ErrOperatorNotFound,
		})
	}
	return &Session{
		ID:         id,
		OperatorID: operatorID,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  expires,
		IP:         ip,
		UserAgent:  userAgent,
	}, nil
}

func (s *SQLiteStore) GetSession(ctx context.Context, id string) (*Session, error) {
	// Join against operators so a session whose operator was removed is
	// invisible even if the CASCADE FK hasn't run yet (defense in depth).
	row := s.db.QueryRowContext(ctx,
		`SELECT s.id, s.operator_id, s.created_at, s.last_seen_at, s.expires_at, s.ip, s.user_agent
		 FROM sessions s
		 INNER JOIN operators o ON o.id = s.operator_id
		 WHERE s.id = ?`, id)
	sess, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("get session: %w", ErrSessionNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	return sess, nil
}

// TouchSession sets last_seen_at to `now`, but only when `now` is strictly
// after the existing last_seen_at. Backward writes (clock rollback, test
// fakes that rewind time) are silently ignored so an attacker cannot revive
// an idle-expired session by submitting an earlier timestamp.
func (s *SQLiteStore) TouchSession(ctx context.Context, id string, now time.Time) error {
	nowStr := now.UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET last_seen_at = ?
		 WHERE id = ? AND last_seen_at < ?`,
		nowStr, id, nowStr)
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("touch session rows affected: %w", err)
	}
	if n == 0 {
		// Either the session doesn't exist OR the existing last_seen_at is
		// already >= now. Distinguish via a follow-up existence probe so
		// callers that legitimately re-touch within the same second don't
		// see a spurious ErrSessionNotFound.
		var exists int
		if err := s.db.QueryRowContext(ctx,
			`SELECT 1 FROM sessions WHERE id = ?`, id).Scan(&exists); err != nil {
			return fmt.Errorf("touch session: %w", ErrSessionNotFound)
		}
		return nil
	}
	return nil
}

func (s *SQLiteStore) DeleteSession(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (s *SQLiteStore) DeleteOperatorSessions(ctx context.Context, operatorID string) error {
	// Defensive guard: an empty operator id is always a programming error,
	// and silently no-oping it (or, worse, deleting orphan rows if data
	// invariants ever drift) would mask the bug.
	if operatorID == "" {
		return fmt.Errorf("delete operator sessions: operator id is empty")
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE operator_id = ?`, operatorID); err != nil {
		return fmt.Errorf("delete operator sessions: %w", err)
	}
	return nil
}

func scanSession(s scanner) (*Session, error) {
	var (
		sess                                Session
		createdStr, lastSeenStr, expiresStr string
		ip, ua                              sql.NullString
	)
	if err := s.Scan(&sess.ID, &sess.OperatorID, &createdStr, &lastSeenStr, &expiresStr, &ip, &ua); err != nil {
		return nil, err
	}
	t, err := time.Parse(time.RFC3339, createdStr)
	if err != nil {
		return nil, fmt.Errorf("parse session created_at: %w", err)
	}
	sess.CreatedAt = t
	t, err = time.Parse(time.RFC3339, lastSeenStr)
	if err != nil {
		return nil, fmt.Errorf("parse session last_seen_at: %w", err)
	}
	sess.LastSeenAt = t
	t, err = time.Parse(time.RFC3339, expiresStr)
	if err != nil {
		return nil, fmt.Errorf("parse session expires_at: %w", err)
	}
	sess.ExpiresAt = t
	if ip.Valid {
		sess.IP = ip.String
	}
	if ua.Valid {
		sess.UserAgent = ua.String
	}
	return &sess, nil
}
