package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS subscription_key (
	id          TEXT PRIMARY KEY,
	component   TEXT NOT NULL,
	label       TEXT NOT NULL,
	active      INTEGER NOT NULL DEFAULT 1,
	created_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
	expires_at  DATETIME,
	usage_count INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_subscription_key_component ON subscription_key(component);
`

// SQLiteStore implements KeyStore using modernc.org/sqlite (pure Go, no CGo).
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore opens a SQLite database at the given path (or ":memory:" for tests),
// applies required PRAGMAs, and runs schema migrations.
func NewSQLiteStore(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// Single writer is required for WAL mode correctness.
	db.SetMaxOpenConns(1)

	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("exec %s: %w", p, err)
		}
	}

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}

	return &SQLiteStore{db: db}, nil
}

// Close releases the database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// CreateKey generates a new subscription key and persists it.
func (s *SQLiteStore) CreateKey(ctx context.Context, component, label string, expiresAt *time.Time) (*Key, error) {
	id, err := generateKeyValue()
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}

	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO subscription_key (id, component, label, active, created_at, expires_at, usage_count)
		 VALUES (?, ?, ?, 1, ?, ?, 0)`,
		id, component, label, now.Format(time.RFC3339), formatNullTime(expiresAt),
	)
	if err != nil {
		return nil, fmt.Errorf("insert key: %w", err)
	}

	return &Key{
		ID:         id,
		Component:  component,
		Label:      label,
		Active:     true,
		CreatedAt:  now,
		ExpiresAt:  expiresAt,
		UsageCount: 0,
	}, nil
}

// GetByValue retrieves a key by its value (id column).
// Returns ErrNotFound if the key does not exist, ErrRevoked if active=0.
func (s *SQLiteStore) GetByValue(ctx context.Context, value string) (*Key, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, component, label, active, created_at, expires_at, usage_count
		 FROM subscription_key WHERE id = ?`, value)

	k, err := scanKey(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("get key: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get key: %w", err)
	}

	if !k.Active {
		return nil, fmt.Errorf("get key: %w", ErrRevoked)
	}

	return k, nil
}

// GetByID retrieves a key by ID regardless of its active status.
// Returns ErrNotFound if the key does not exist.
// Unlike GetByValue, revoked keys (active=0) are returned without error.
func (s *SQLiteStore) GetByID(ctx context.Context, id string) (*Key, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, component, label, active, created_at, expires_at, usage_count
		 FROM subscription_key WHERE id = ?`, id)

	k, err := scanKey(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("get key: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get key: %w", err)
	}

	return k, nil // no Active check — returns revoked keys too
}

// ListKeys returns all keys, optionally filtered by component (empty string = all).
func (s *SQLiteStore) ListKeys(ctx context.Context, component string) ([]*Key, error) {
	var (
		rows *sql.Rows
		err  error
	)

	if component == "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, component, label, active, created_at, expires_at, usage_count
			 FROM subscription_key ORDER BY created_at DESC`)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, component, label, active, created_at, expires_at, usage_count
			 FROM subscription_key WHERE component = ? ORDER BY created_at DESC`, component)
	}
	if err != nil {
		return nil, fmt.Errorf("list keys: %w", err)
	}
	defer rows.Close()

	var keys []*Key
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, fmt.Errorf("scan key: %w", err)
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list keys rows: %w", err)
	}

	return keys, nil
}

// RevokeKey sets active=0 for the given key id.
func (s *SQLiteStore) RevokeKey(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE subscription_key SET active = 0 WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("revoke key: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("revoke key rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("revoke key: %w", ErrNotFound)
	}
	return nil
}

// IncrementUsage atomically increments the usage_count for the given key id.
func (s *SQLiteStore) IncrementUsage(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE subscription_key SET usage_count = usage_count + 1 WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("increment usage: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("increment usage rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("increment usage: %w", ErrNotFound)
	}
	return nil
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanKey(s scanner) (*Key, error) {
	var (
		k          Key
		activeInt  int
		createdStr string
		expiresStr sql.NullString
	)
	err := s.Scan(&k.ID, &k.Component, &k.Label, &activeInt, &createdStr, &expiresStr, &k.UsageCount)
	if err != nil {
		return nil, err
	}
	k.Active = activeInt == 1

	t, err := time.Parse(time.RFC3339, createdStr)
	if err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	k.CreatedAt = t

	if expiresStr.Valid {
		t2, err := time.Parse(time.RFC3339, expiresStr.String)
		if err != nil {
			return nil, fmt.Errorf("parse expires_at: %w", err)
		}
		k.ExpiresAt = &t2
	}

	return &k, nil
}

func generateKeyValue() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func formatNullTime(t *time.Time) sql.NullString {
	if t == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: t.UTC().Format(time.RFC3339), Valid: true}
}
