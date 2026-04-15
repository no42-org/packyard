package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
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

CREATE TABLE IF NOT EXISTS components (
	name              TEXT PRIMARY KEY,
	visibility        TEXT NOT NULL DEFAULT 'private'
	                  CHECK (visibility IN ('public', 'private')),
	rpm_series        TEXT NOT NULL DEFAULT '[]',
	rpm_os_families   TEXT NOT NULL DEFAULT '[]',
	rpm_architectures TEXT NOT NULL DEFAULT '[]',
	created_at        DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);
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

// ─── ComponentStore implementation ───────────────────────────────────────────

// CreateComponent inserts a new component record.
// Returns ErrComponentExists if a component with the same name already exists.
func (s *SQLiteStore) CreateComponent(ctx context.Context, comp *Component) (*Component, error) {
	seriesJSON, err := json.Marshal(comp.RPMSeries)
	if err != nil {
		return nil, fmt.Errorf("marshal rpm_series: %w", err)
	}
	familiesJSON, err := json.Marshal(comp.RPMOSFamilies)
	if err != nil {
		return nil, fmt.Errorf("marshal rpm_os_families: %w", err)
	}
	archsJSON, err := json.Marshal(comp.RPMArchitectures)
	if err != nil {
		return nil, fmt.Errorf("marshal rpm_architectures: %w", err)
	}

	vis := comp.Visibility
	if vis == "" {
		vis = "private"
	}

	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO components (name, visibility, rpm_series, rpm_os_families, rpm_architectures, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		comp.Name, vis, string(seriesJSON), string(familiesJSON), string(archsJSON),
		now.Format(time.RFC3339),
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return nil, fmt.Errorf("create component: %w", ErrComponentExists)
		}
		return nil, fmt.Errorf("create component: %w", err)
	}

	return &Component{
		Name:             comp.Name,
		Visibility:       vis,
		RPMSeries:        comp.RPMSeries,
		RPMOSFamilies:    comp.RPMOSFamilies,
		RPMArchitectures: comp.RPMArchitectures,
		CreatedAt:        now,
	}, nil
}

// GetComponent retrieves a component by name.
// Returns ErrComponentNotFound if it does not exist.
func (s *SQLiteStore) GetComponent(ctx context.Context, name string) (*Component, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT name, visibility, rpm_series, rpm_os_families, rpm_architectures, created_at
		 FROM components WHERE name = ?`, name)
	c, err := scanComponent(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("get component: %w", ErrComponentNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get component: %w", err)
	}
	return c, nil
}

// ListComponents returns all components ordered by name ascending.
func (s *SQLiteStore) ListComponents(ctx context.Context) ([]*Component, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, visibility, rpm_series, rpm_os_families, rpm_architectures, created_at
		 FROM components ORDER BY name ASC`)
	if err != nil {
		return nil, fmt.Errorf("list components: %w", err)
	}
	defer rows.Close()

	var comps []*Component
	for rows.Next() {
		c, err := scanComponent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan component: %w", err)
		}
		comps = append(comps, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list components rows: %w", err)
	}
	if comps == nil {
		comps = []*Component{}
	}
	return comps, nil
}

// DeleteComponent removes the component record by name.
// Returns ErrComponentNotFound if the component does not exist.
func (s *SQLiteStore) DeleteComponent(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM components WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("delete component: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete component rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("delete component: %w", ErrComponentNotFound)
	}
	return nil
}

// RevokeComponentKeys sets active=0 for all keys scoped to the given component.
// Returns the number of keys revoked.
func (s *SQLiteStore) RevokeComponentKeys(ctx context.Context, component string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE subscription_key SET active = 0 WHERE component = ? AND active = 1`, component)
	if err != nil {
		return 0, fmt.Errorf("revoke component keys: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("revoke component keys rows affected: %w", err)
	}
	return n, nil
}

// CountActiveComponentKeys returns the number of active keys scoped to the given component.
func (s *SQLiteStore) CountActiveComponentKeys(ctx context.Context, component string) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM subscription_key WHERE component = ? AND active = 1`, component).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count component keys: %w", err)
	}
	return count, nil
}

// DeleteComponentWithRevoke atomically revokes all active keys for the component
// and deletes the component record in a single transaction.
// Returns the number of keys revoked.
// Returns ErrComponentNotFound if the component does not exist.
func (s *SQLiteStore) DeleteComponentWithRevoke(ctx context.Context, name string) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	res, err := tx.ExecContext(ctx,
		`UPDATE subscription_key SET active = 0 WHERE component = ? AND active = 1`, name)
	if err != nil {
		return 0, fmt.Errorf("revoke component keys: %w", err)
	}
	revoked, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("revoke component keys rows affected: %w", err)
	}

	res2, err := tx.ExecContext(ctx, `DELETE FROM components WHERE name = ?`, name)
	if err != nil {
		return 0, fmt.Errorf("delete component: %w", err)
	}
	n, err := res2.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("delete component rows affected: %w", err)
	}
	if n == 0 {
		return 0, fmt.Errorf("delete component: %w", ErrComponentNotFound)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return revoked, nil
}

// UpdateComponentVisibility sets the visibility field for a component.
// Returns the updated component record or ErrComponentNotFound if the component does not exist.
// Uses RETURNING to read back the row in the same statement, avoiding a TOCTOU between
// the UPDATE and a subsequent SELECT.
func (s *SQLiteStore) UpdateComponentVisibility(ctx context.Context, name, visibility string) (*Component, error) {
	row := s.db.QueryRowContext(ctx,
		`UPDATE components SET visibility = ? WHERE name = ?
		 RETURNING name, visibility, rpm_series, rpm_os_families, rpm_architectures, created_at`,
		visibility, name)
	c, err := scanComponent(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("update component visibility: %w", ErrComponentNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("update component visibility: %w", err)
	}
	return c, nil
}

// LoadComponentSets queries the components table and returns two maps for O(1) lookups:
//   - validComponents: all component names
//   - publicComponents: component names with visibility="public"
func (s *SQLiteStore) LoadComponentSets(ctx context.Context) (valid map[string]bool, public map[string]bool, err error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, visibility FROM components ORDER BY name ASC`)
	if err != nil {
		return nil, nil, fmt.Errorf("load component sets: %w", err)
	}
	defer rows.Close()

	valid = make(map[string]bool)
	public = make(map[string]bool)
	for rows.Next() {
		var name, vis string
		if err := rows.Scan(&name, &vis); err != nil {
			return nil, nil, fmt.Errorf("scan component set: %w", err)
		}
		valid[name] = true
		if vis == "public" {
			public[name] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("load component sets rows: %w", err)
	}
	return valid, public, nil
}

type componentScanner interface {
	Scan(dest ...any) error
}

func scanComponent(s componentScanner) (*Component, error) {
	var (
		c          Component
		createdStr string
		seriesRaw  string
		familyRaw  string
		archRaw    string
	)
	if err := s.Scan(&c.Name, &c.Visibility, &seriesRaw, &familyRaw, &archRaw, &createdStr); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(seriesRaw), &c.RPMSeries); err != nil {
		return nil, fmt.Errorf("unmarshal rpm_series: %w", err)
	}
	if err := json.Unmarshal([]byte(familyRaw), &c.RPMOSFamilies); err != nil {
		return nil, fmt.Errorf("unmarshal rpm_os_families: %w", err)
	}
	if err := json.Unmarshal([]byte(archRaw), &c.RPMArchitectures); err != nil {
		return nil, fmt.Errorf("unmarshal rpm_architectures: %w", err)
	}
	t, err := time.Parse(time.RFC3339, createdStr)
	if err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	c.CreatedAt = t
	return &c, nil
}
