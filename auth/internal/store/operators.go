/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"time"
)

// generateOperatorID returns a 32-char hex id (16 random bytes), same shape as
// account ids.
func generateOperatorID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *SQLiteStore) GetOperator(ctx context.Context, id string) (*Operator, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, email, role, status, allowlisted_at, allowlisted_by,
		        last_login_at, github_username, microsoft_upn, first_seen_provider
		 FROM operators WHERE id = ?`, id)
	op, err := scanOperator(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("get operator: %w", ErrOperatorNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get operator: %w", err)
	}
	return op, nil
}

func (s *SQLiteStore) GetOperatorByEmail(ctx context.Context, email string) (*Operator, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, email, role, status, allowlisted_at, allowlisted_by,
		        last_login_at, github_username, microsoft_upn, first_seen_provider
		 FROM operators WHERE email = ?`, canonicalEmail(email))
	op, err := scanOperator(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("get operator by email: %w", ErrOperatorNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get operator by email: %w", err)
	}
	return op, nil
}

func (s *SQLiteStore) AllowlistOperator(ctx context.Context, email string, role OperatorRole, allowlistedBy string) (*Operator, error) {
	id, err := generateOperatorID()
	if err != nil {
		return nil, fmt.Errorf("generate operator id: %w", err)
	}
	canon := canonicalEmail(email)
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO operators (id, email, role, status, allowlisted_at, allowlisted_by)
		 VALUES (?, ?, ?, 'active', ?, ?)`,
		id, canon, string(role), now.Format(time.RFC3339), allowlistedBy,
	)
	if err != nil {
		return nil, classify("allowlist operator", err, constraintErrorMap{
			UniqueErr: ErrOperatorExists,
			CheckErr:  ErrOperatorInvalid,
		})
	}
	return &Operator{
		ID:            id,
		Email:         canon,
		Role:          role,
		Status:        OperatorStatusActive,
		AllowlistedAt: now,
		AllowlistedBy: allowlistedBy,
	}, nil
}

func (s *SQLiteStore) DisableOperator(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE operators SET status = 'disabled' WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("disable operator: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("disable operator rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("disable operator: %w", ErrOperatorNotFound)
	}
	return nil
}

// EnableOperator flips status back to 'active'. Used by an admin
// reactivating a peer (and indirectly by ChangeRole's promote-then-enable
// sequence if a future caller needs it).
func (s *SQLiteStore) EnableOperator(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE operators SET status = 'active' WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("enable operator: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("enable operator rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("enable operator: %w", ErrOperatorNotFound)
	}
	return nil
}

// ChangeOperatorRole sets the operator's role. Rejects unknown role
// values via the CHECK constraint on the column (returns a generic error).
func (s *SQLiteStore) ChangeOperatorRole(ctx context.Context, id string, role OperatorRole) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE operators SET role = ? WHERE id = ?`, string(role), id)
	if err != nil {
		return fmt.Errorf("change operator role: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("change operator role rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("change operator role: %w", ErrOperatorNotFound)
	}
	return nil
}

// ListOperators returns operators ordered by allowlisted_at DESC, paginated.
func (s *SQLiteStore) ListOperators(ctx context.Context, offset, limit int) ([]*Operator, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, email, role, status, allowlisted_at, allowlisted_by,
		        last_login_at, github_username, microsoft_upn, first_seen_provider
		 FROM operators
		 ORDER BY allowlisted_at DESC, id DESC
		 LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list operators: %w", err)
	}
	defer rows.Close()

	out := make([]*Operator, 0)
	for rows.Next() {
		op, err := scanOperator(rows)
		if err != nil {
			return nil, fmt.Errorf("scan operator: %w", err)
		}
		out = append(out, op)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list operators rows: %w", err)
	}
	return out, nil
}

// CountActiveAdmins returns the number of operators with role='admin' AND
// status='active'. The production self-lockout guard inside
// UpdateOperatorAtomically does its own in-transaction count; this exported
// helper exists for tests and metrics callers that need a point-in-time
// snapshot.
func (s *SQLiteStore) CountActiveAdmins(ctx context.Context) (int64, error) {
	var n int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM operators WHERE role = 'admin' AND status = 'active'`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count active admins: %w", err)
	}
	return n, nil
}

// UpdateOperatorAtomically applies an optional role + status change inside a
// single serializable transaction. The global self-lockout guard runs inside
// the same transaction: if the mutation removes an active admin and would
// leave zero active admins, the transaction rolls back and the function
// returns ErrOperatorSelfLockout. Returns the before/after rows so the
// handler can emit accurate audit transitions and decide on force-logout.
//
// The guard is global, not actor-scoped: it doesn't matter who is making the
// change — the invariant "≥1 active admin" is what's protected. This closes
// the asymmetry where an admin could demote/disable the last *other* admin.
func (s *SQLiteStore) UpdateOperatorAtomically(ctx context.Context, id string,
	newRole *OperatorRole, newStatus *OperatorStatus,
) (*Operator, *Operator, error) {
	if newRole == nil && newStatus == nil {
		return nil, nil, fmt.Errorf("update operator: %w", ErrOperatorInvalid)
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, nil, fmt.Errorf("update operator begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	row := tx.QueryRowContext(ctx,
		`SELECT id, email, role, status, allowlisted_at, allowlisted_by,
		        last_login_at, github_username, microsoft_upn, first_seen_provider
		 FROM operators WHERE id = ?`, id)
	before, err := scanOperator(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, fmt.Errorf("update operator: %w", ErrOperatorNotFound)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("update operator read before: %w", err)
	}

	afterRole := before.Role
	afterStatus := before.Status
	if newRole != nil {
		afterRole = *newRole
	}
	if newStatus != nil {
		afterStatus = *newStatus
	}

	wasAdminActive := before.Role == OperatorRoleAdmin && before.Status == OperatorStatusActive
	willBeAdminActive := afterRole == OperatorRoleAdmin && afterStatus == OperatorStatusActive
	if wasAdminActive && !willBeAdminActive {
		var n int64
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM operators WHERE role = 'admin' AND status = 'active'`).Scan(&n); err != nil {
			return nil, nil, fmt.Errorf("update operator count admins: %w", err)
		}
		if n <= 1 {
			return nil, nil, fmt.Errorf("update operator: %w", ErrOperatorSelfLockout)
		}
	}

	switch {
	case newRole != nil && newStatus != nil:
		if _, err := tx.ExecContext(ctx,
			`UPDATE operators SET role = ?, status = ? WHERE id = ?`,
			string(*newRole), string(*newStatus), id); err != nil {
			return nil, nil, fmt.Errorf("update operator role+status: %w", err)
		}
	case newRole != nil:
		if _, err := tx.ExecContext(ctx,
			`UPDATE operators SET role = ? WHERE id = ?`,
			string(*newRole), id); err != nil {
			return nil, nil, fmt.Errorf("update operator role: %w", err)
		}
	case newStatus != nil:
		if _, err := tx.ExecContext(ctx,
			`UPDATE operators SET status = ? WHERE id = ?`,
			string(*newStatus), id); err != nil {
			return nil, nil, fmt.Errorf("update operator status: %w", err)
		}
	}

	afterRow := tx.QueryRowContext(ctx,
		`SELECT id, email, role, status, allowlisted_at, allowlisted_by,
		        last_login_at, github_username, microsoft_upn, first_seen_provider
		 FROM operators WHERE id = ?`, id)
	after, err := scanOperator(afterRow)
	if err != nil {
		return nil, nil, fmt.Errorf("update operator read after: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("update operator commit: %w", err)
	}
	return before, after, nil
}

// UpdateLastLogin records the operator's most recent OAuth login success.
// Called from the OAuth callback handler after session insertion. Only
// advances last_login_at forward — a backward clock step (NTP rollback,
// leap-smear correction) must not surface in the operator-management UI as
// a regression that looks like tampering. Mirrors TouchSession's monotonic
// guard. The query is still RowsAffected-comparable: when the operator row
// exists, the WHERE matches; the predicate fails only when `ts` does not
// move the column forward, which is a no-op (not an error).
func (s *SQLiteStore) UpdateLastLogin(ctx context.Context, id string, ts time.Time) error {
	tsStr := ts.UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx,
		`UPDATE operators
		 SET last_login_at = ?
		 WHERE id = ?
		   AND (last_login_at IS NULL OR last_login_at < ?)`,
		tsStr, id, tsStr)
	if err != nil {
		return fmt.Errorf("update last_login_at: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update last_login_at rows affected: %w", err)
	}
	if n == 0 {
		// Either the operator row doesn't exist OR the stored timestamp is
		// already >= ts (monotonic no-op). Distinguish with a follow-up
		// existence probe so the caller still gets ErrOperatorNotFound when
		// it matters.
		var exists int
		if err := s.db.QueryRowContext(ctx,
			`SELECT 1 FROM operators WHERE id = ?`, id).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("update last_login_at: %w", ErrOperatorNotFound)
			}
			return fmt.Errorf("update last_login_at existence probe: %w", err)
		}
		// Operator exists but ts didn't move the column forward — no-op.
	}
	return nil
}

// UpdateLoginProvider opportunistically captures the provider-specific
// identity (github_username or microsoft_upn) and sets first_seen_provider
// on first OAuth login from that provider per D14. Idempotent:
//   - first_seen_provider is only set when currently NULL
//   - the provider-specific column is only set when currently NULL
//
// Calling this with an unknown providerName is a caller bug and is a no-op
// (the CHECK constraint on first_seen_provider would otherwise reject it).
func (s *SQLiteStore) UpdateLoginProvider(ctx context.Context, id, providerName, providerUserID string) error {
	if providerUserID == "" {
		return nil
	}
	var column string
	switch providerName {
	case "github":
		column = "github_username"
	case "microsoft":
		column = "microsoft_upn"
	default:
		return fmt.Errorf("update login provider: unknown provider %q", providerName)
	}
	// COALESCE so existing values are preserved (first-seen semantics).
	q := fmt.Sprintf(
		`UPDATE operators SET %s = COALESCE(%s, ?), first_seen_provider = COALESCE(first_seen_provider, ?) WHERE id = ?`,
		column, column,
	)
	if _, err := s.db.ExecContext(ctx, q, providerUserID, providerName, id); err != nil {
		return fmt.Errorf("update login provider: %w", err)
	}
	return nil
}

func (s *SQLiteStore) OperatorCount(ctx context.Context) (int64, error) {
	var n int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM operators`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count operators: %w", err)
	}
	return n, nil
}

// BootstrapOperatorFromEnv implements § 5.4: if the operators table is empty
// AND the email argument is non-empty, insert it as the first admin operator.
// Idempotent and inert if any operator already exists. Returns the inserted
// operator (or nil if no insert was made).
//
// The count and insert run inside a serializable transaction so two startup
// paths racing for the empty-table state can't both pass the count=0 check.
// With `db.SetMaxOpenConns(1)` this is already serialised, but the explicit
// isolation removes the load-bearing assumption.
func (s *SQLiteStore) BootstrapOperatorFromEnv(ctx context.Context, email string) (*Operator, error) {
	if email == "" {
		return nil, nil
	}
	// Validate the env-var value parses as an RFC 5322 mailbox before insert.
	// The CHECK constraint on the column is just `LIKE '%_@_%'`, which lets
	// through near-useless values like `a@b` — those would consume the first
	// admin slot and require a direct DB edit to recover from. Surface the
	// typo at startup instead.
	if _, err := mail.ParseAddress(email); err != nil {
		return nil, fmt.Errorf("bootstrap operator: %w (got %q)", ErrOperatorInvalid, email)
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, fmt.Errorf("bootstrap operator: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var n int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM operators`).Scan(&n); err != nil {
		return nil, fmt.Errorf("bootstrap operator: %w", err)
	}
	if n > 0 {
		return nil, nil
	}

	id, err := generateOperatorID()
	if err != nil {
		return nil, fmt.Errorf("bootstrap operator: %w", err)
	}
	canon := canonicalEmail(email)
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO operators (id, email, role, status, allowlisted_at, allowlisted_by)
		 VALUES (?, ?, ?, 'active', ?, ?)`,
		id, canon, string(OperatorRoleAdmin), now.Format(time.RFC3339), "bootstrap",
	); err != nil {
		return nil, classify("bootstrap operator", err, constraintErrorMap{
			UniqueErr: ErrOperatorExists,
			CheckErr:  ErrOperatorInvalid,
		})
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("bootstrap operator: %w", err)
	}

	return &Operator{
		ID:            id,
		Email:         canon,
		Role:          OperatorRoleAdmin,
		Status:        OperatorStatusActive,
		AllowlistedAt: now,
		AllowlistedBy: "bootstrap",
	}, nil
}

func scanOperator(s scanner) (*Operator, error) {
	var (
		op                Operator
		role, status      string
		allowlistedAt     string
		allowlistedBy     sql.NullString
		lastLoginAt       sql.NullString
		githubUsername    sql.NullString
		microsoftUPN      sql.NullString
		firstSeenProvider sql.NullString
	)
	if err := s.Scan(&op.ID, &op.Email, &role, &status, &allowlistedAt,
		&allowlistedBy, &lastLoginAt, &githubUsername, &microsoftUPN, &firstSeenProvider); err != nil {
		return nil, err
	}
	op.Role = OperatorRole(role)
	op.Status = OperatorStatus(status)
	t, err := time.Parse(time.RFC3339, allowlistedAt)
	if err != nil {
		return nil, fmt.Errorf("parse allowlisted_at: %w", err)
	}
	op.AllowlistedAt = t
	if allowlistedBy.Valid {
		op.AllowlistedBy = allowlistedBy.String
	}
	if lastLoginAt.Valid {
		lt, err := time.Parse(time.RFC3339, lastLoginAt.String)
		if err != nil {
			return nil, fmt.Errorf("parse last_login_at: %w", err)
		}
		op.LastLoginAt = &lt
	}
	if githubUsername.Valid {
		op.GithubUsername = githubUsername.String
	}
	if microsoftUPN.Valid {
		op.MicrosoftUPN = microsoftUPN.String
	}
	if firstSeenProvider.Valid {
		op.FirstSeenProvider = firstSeenProvider.String
	}
	return &op, nil
}
