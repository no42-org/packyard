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
	"time"
)

// generateAccountID returns a 32-char hex id (16 random bytes). Shorter than
// subscription_key ids — accounts are listed/typed by operators, keys are not.
func generateAccountID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// CreateAccount inserts a new account, generating the id server-side. The
// email is canonicalised before insert; the schema CHECK enforces the same
// invariant so a writer bypassing canonicalEmail still fails closed.
func (s *SQLiteStore) CreateAccount(ctx context.Context, in AccountInput, createdByOperatorID string) (*Account, error) {
	id, err := generateAccountID()
	if err != nil {
		return nil, fmt.Errorf("generate account id: %w", err)
	}

	email := canonicalEmail(in.Email)
	now := time.Now().UTC()

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO accounts (id, email, org_name, status, created_at, created_by_operator_id)
		 VALUES (?, ?, ?, 'active', ?, ?)`,
		id, email, in.OrgName, now.Format(time.RFC3339), createdByOperatorID,
	)
	if err != nil {
		return nil, classifyConstraintError("create account", err)
	}

	return &Account{
		ID:                  id,
		Email:               email,
		OrgName:             in.OrgName,
		Status:              AccountStatusActive,
		CreatedAt:           now,
		CreatedByOperatorID: createdByOperatorID,
	}, nil
}

// GetAccount returns the account by id. Returns ErrAccountNotFound if no row
// exists OR if the account is soft-deleted (spec: "deleted accounts are not
// visible by id").
func (s *SQLiteStore) GetAccount(ctx context.Context, id string) (*Account, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, email, org_name, status, created_at, created_by_operator_id
		 FROM accounts WHERE id = ?`, id)

	a, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("get account: %w", ErrAccountNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get account: %w", err)
	}
	if a.Status == AccountStatusDeleted {
		return nil, fmt.Errorf("get account: %w", ErrAccountNotFound)
	}
	return a, nil
}

// ListAccounts returns accounts ordered by created_at DESC. A "" status
// excludes deleted rows; a specific status returns only matches.
func (s *SQLiteStore) ListAccounts(ctx context.Context, statusFilter AccountStatus, offset, limit int) ([]*Account, error) {
	var (
		rows *sql.Rows
		err  error
	)

	const cols = `id, email, org_name, status, created_at, created_by_operator_id`

	if statusFilter == "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+cols+`
			 FROM accounts WHERE status != 'deleted'
			 ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`,
			limit, offset)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+cols+`
			 FROM accounts WHERE status = ?
			 ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`,
			string(statusFilter), limit, offset)
	}
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	defer rows.Close()

	out := make([]*Account, 0)
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("scan account: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list accounts rows: %w", err)
	}
	return out, nil
}

// UpdateAccount applies a partial update and returns the resulting row using
// RETURNING to avoid TOCTOU between UPDATE and SELECT. nil fields in upd are
// left unchanged via COALESCE.
//
// The caller (handler) is responsible for enforcing status-transition rules
// (active↔suspended only; the deleted target uses DELETE). The store accepts
// any of the three statuses to keep the layer simple; transitioning to
// 'deleted' here bypasses cascade-revoke and is intentionally not exposed by
// the handler.
func (s *SQLiteStore) UpdateAccount(ctx context.Context, id string, upd AccountUpdate) (*Account, error) {
	// Defence in depth: the handler must enforce active↔suspended transitions
	// (deleted goes through DELETE). The store refuses target='deleted' so a
	// non-handler caller (bulk import, test, script) can't soft-delete an
	// account without cascading the key revoke.
	if upd.Status != nil && *upd.Status == AccountStatusDeleted {
		return nil, fmt.Errorf("update account: %w", ErrInvalidStatusTransition)
	}
	// Legacy account is protected from suspend (would deny every legacy key)
	// and from email/org_name renames (a rename would let an operator collide
	// the legacy id with a real customer's email, defeating uniqueness checks
	// invisibly).
	if id == legacyAccountID {
		if upd.Status != nil && *upd.Status != AccountStatusActive {
			return nil, fmt.Errorf("update account: %w", ErrLegacyAccountProtected)
		}
		if upd.Email != nil || upd.OrgName != nil {
			return nil, fmt.Errorf("update account: %w", ErrLegacyAccountProtected)
		}
	}

	const sqlText = `
		UPDATE accounts
		SET email     = COALESCE(?, email),
		    org_name  = COALESCE(?, org_name),
		    status    = COALESCE(?, status)
		WHERE id = ?
		RETURNING id, email, org_name, status, created_at, created_by_operator_id`

	var emailArg, orgArg, statusArg any
	if upd.Email != nil {
		emailArg = canonicalEmail(*upd.Email)
	}
	if upd.OrgName != nil {
		orgArg = *upd.OrgName
	}
	if upd.Status != nil {
		statusArg = string(*upd.Status)
	}

	row := s.db.QueryRowContext(ctx, sqlText, emailArg, orgArg, statusArg, id)
	a, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("update account: %w", ErrAccountNotFound)
	}
	if err != nil {
		return nil, classifyConstraintError("update account", err)
	}
	return a, nil
}

// DeleteAccountWithCascade revokes every active key the account owns and sets
// account.status='deleted' atomically. Returns the count of keys revoked.
// Returns ErrAccountNotFound if the account does not exist or is already
// deleted (idempotent failure — the caller should treat both as "no-op").
func (s *SQLiteStore) DeleteAccountWithCascade(ctx context.Context, id string) (int64, error) {
	// Legacy account is the FK target for every pre-account-lifecycle key;
	// deleting it would cascade-revoke them all.
	if id == legacyAccountID {
		return 0, fmt.Errorf("delete account: %w", ErrLegacyAccountProtected)
	}

	// LevelSerializable maps to SQLite's BEGIN IMMEDIATE, acquiring the
	// RESERVED lock up front so the SELECT-status-then-UPDATE pattern is not
	// racy against concurrent writers. Today MaxOpenConns=1 already serialises
	// everything; the explicit isolation removes the load-bearing assumption.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var status string
	if err := tx.QueryRowContext(ctx,
		`SELECT status FROM accounts WHERE id = ?`, id).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("delete account: %w", ErrAccountNotFound)
		}
		return 0, fmt.Errorf("delete account lookup: %w", err)
	}
	if status == string(AccountStatusDeleted) {
		return 0, fmt.Errorf("delete account: %w", ErrAccountNotFound)
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE subscription_key SET active = 0
		 WHERE account_id = ? AND active = 1`, id)
	if err != nil {
		return 0, fmt.Errorf("cascade revoke keys: %w", err)
	}
	revoked, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("cascade revoke rows affected: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE accounts SET status = 'deleted' WHERE id = ?`, id); err != nil {
		return 0, fmt.Errorf("mark account deleted: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit delete: %w", err)
	}
	return revoked, nil
}

// CountActiveAccountKeys returns the count of active keys owned by id. Used
// by the DELETE impact preview.
func (s *SQLiteStore) CountActiveAccountKeys(ctx context.Context, id string) (int64, error) {
	var n int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM subscription_key WHERE account_id = ? AND active = 1`,
		id).Scan(&n); err != nil {
		return 0, fmt.Errorf("count active account keys: %w", err)
	}
	return n, nil
}

// ListAccountKeys returns keys (active + revoked) owned by id, ordered by
// created_at DESC. Caller clamps offset/limit to D23 bounds.
func (s *SQLiteStore) ListAccountKeys(ctx context.Context, id string, offset, limit int) ([]*Key, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, component, label, active, created_at, expires_at, usage_count, account_id
		 FROM subscription_key WHERE account_id = ?
		 ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, id, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list account keys: %w", err)
	}
	defer rows.Close()

	out := make([]*Key, 0)
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, fmt.Errorf("scan account key: %w", err)
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list account keys rows: %w", err)
	}
	return out, nil
}

// CreateKeyForAccount provisions a subscription key under the given account.
// The FK ON DELETE RESTRICT (and the NOT NULL on account_id) means inserting
// against a non-existent account fails at the DB layer; the handler maps that
// to a 404.
func (s *SQLiteStore) CreateKeyForAccount(ctx context.Context, accountID, component, label string, expiresAt *time.Time) (*Key, error) {
	id, err := generateKeyValue()
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}

	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO subscription_key (id, component, label, active, created_at, expires_at, usage_count, account_id)
		 VALUES (?, ?, ?, 1, ?, ?, 0, ?)`,
		id, component, label, now.Format(time.RFC3339), formatNullTime(expiresAt), accountID,
	)
	if err != nil {
		return nil, classifyConstraintError("create key for account", err)
	}

	return &Key{
		ID:         id,
		Component:  component,
		Label:      label,
		Active:     true,
		CreatedAt:  now,
		ExpiresAt:  expiresAt,
		UsageCount: 0,
		AccountID:  accountID,
	}, nil
}

func scanAccount(s scanner) (*Account, error) {
	var (
		a          Account
		createdStr string
		orgName    sql.NullString
		status     string
	)
	if err := s.Scan(&a.ID, &a.Email, &orgName, &status, &createdStr, &a.CreatedByOperatorID); err != nil {
		return nil, err
	}
	if orgName.Valid {
		a.OrgName = orgName.String
	}
	a.Status = AccountStatus(status)
	t, err := time.Parse(time.RFC3339, createdStr)
	if err != nil {
		return nil, fmt.Errorf("parse account created_at: %w", err)
	}
	a.CreatedAt = t
	return &a, nil
}
