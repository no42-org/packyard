package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// legacyAccountID is the synthetic account assigned to keys that existed before
// the account aggregate was introduced. Pre-existing keys are backfilled to this
// account by the migration in NewSQLiteStore. Per design D13 (admin-ui-account-lifecycle),
// keys do not transfer between accounts; legacy-owned keys phase out naturally
// as operators issue replacement keys under proper customer accounts.
const legacyAccountID = "legacy"

const schema = `
CREATE TABLE IF NOT EXISTS accounts (
	id                     TEXT PRIMARY KEY,
	email                  TEXT NOT NULL
	                       CHECK (email <> '' AND email = lower(email) AND email LIKE '%_@_%'),
	org_name               TEXT,
	status                 TEXT NOT NULL DEFAULT 'active'
	                       CHECK (status IN ('active', 'suspended', 'deleted')),
	created_at             DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
	created_by_operator_id TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_accounts_email ON accounts(email);

CREATE TABLE IF NOT EXISTS operators (
	id                  TEXT PRIMARY KEY,
	email               TEXT NOT NULL UNIQUE
	                    CHECK (email <> '' AND email = lower(email) AND email LIKE '%_@_%'),
	role                TEXT NOT NULL DEFAULT 'admin'
	                    CHECK (role IN ('admin', 'readonly')),
	status              TEXT NOT NULL DEFAULT 'active'
	                    CHECK (status IN ('active', 'disabled')),
	allowlisted_at      DATETIME NOT NULL,
	allowlisted_by      TEXT,
	last_login_at       DATETIME,
	github_username     TEXT,
	microsoft_upn       TEXT,
	first_seen_provider TEXT CHECK (first_seen_provider IN ('github', 'microsoft'))
);

CREATE TABLE IF NOT EXISTS sessions (
	id            TEXT PRIMARY KEY,
	operator_id   TEXT NOT NULL REFERENCES operators(id) ON DELETE CASCADE,
	created_at    DATETIME NOT NULL,
	last_seen_at  DATETIME NOT NULL,
	expires_at    DATETIME NOT NULL,
	ip            TEXT,
	user_agent    TEXT
);
CREATE INDEX IF NOT EXISTS idx_sessions_operator_id ON sessions(operator_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions(expires_at);

CREATE TABLE IF NOT EXISTS audit_log (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	ts          DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
	operator_id TEXT,
	action      TEXT NOT NULL,
	target_type TEXT,
	target_id   TEXT,
	details     TEXT,
	ip          TEXT,
	user_agent  TEXT
);
CREATE INDEX IF NOT EXISTS idx_audit_log_ts ON audit_log(ts);
CREATE INDEX IF NOT EXISTS idx_audit_log_operator_id ON audit_log(operator_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_action ON audit_log(action);

-- The 'keys' table in spec terms; named subscription_key in the schema.
CREATE TABLE IF NOT EXISTS subscription_key (
	id          TEXT PRIMARY KEY,
	component   TEXT NOT NULL,
	label       TEXT NOT NULL,
	active      INTEGER NOT NULL DEFAULT 1,
	created_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
	expires_at  DATETIME,
	usage_count INTEGER NOT NULL DEFAULT 0,
	account_id  TEXT NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT
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
	// auditLogger is the logger used by the audit Write path for fire-and-
	// forget failure reporting. Per-store rather than package-global so
	// test parallelism and any future multi-instance scenario don't share
	// state. Defaults to slog.Default() via currentAuditLogger.
	auditMu     sync.Mutex
	auditLogger *slog.Logger
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

	if err := seedLegacyAccount(db); err != nil {
		db.Close()
		return nil, err
	}

	if err := migrateSubscriptionKeyAccountID(db); err != nil {
		db.Close()
		return nil, err
	}

	return &SQLiteStore{db: db}, nil
}

// seedLegacyAccount inserts the synthetic 'legacy' account used as the FK target
// for pre-existing keys (and for any new key created before the handler layer
// plumbs account_id through). Idempotent via INSERT OR IGNORE.
func seedLegacyAccount(db *sql.DB) error {
	_, err := db.Exec(
		`INSERT OR IGNORE INTO accounts (id, email, org_name, status, created_by_operator_id)
		 VALUES (?, ?, ?, 'active', 'bootstrap')`,
		legacyAccountID, "legacy@packyard.internal", "Pre-account keys",
	)
	if err != nil {
		return fmt.Errorf("seed legacy account: %w", err)
	}
	return nil
}

// migrateSubscriptionKeyAccountID brings legacy subscription_key tables forward
// to the post-account schema. For fresh installs the table already has the right
// shape and this is a no-op. For existing installs it:
//
//  1. Adds the account_id column (NULLable, with FK) if missing.
//  2. Backfills NULL rows to the legacy account.
//  3. Rebuilds the table with NOT NULL on account_id (task 1.8). The rebuild
//     follows SQLite's recommended sequence for schema changes that NOT NULL'ing
//     a column requires: rename, recreate, copy, drop old.
func migrateSubscriptionKeyAccountID(db *sql.DB) error {
	colExists, colNotNull, err := subscriptionKeyAccountIDColumn(db)
	if err != nil {
		return err
	}

	if !colExists {
		if _, err := db.Exec(
			`ALTER TABLE subscription_key
			 ADD COLUMN account_id TEXT REFERENCES accounts(id) ON DELETE RESTRICT`,
		); err != nil {
			return fmt.Errorf("add account_id column: %w", err)
		}
	}

	// Idempotent index creation — covers both fresh installs (schema string
	// omits this index because existing installs hit it before ADD COLUMN) and
	// the post-ALTER-TABLE path here.
	if _, err := db.Exec(
		`CREATE INDEX IF NOT EXISTS idx_subscription_key_account_id ON subscription_key(account_id)`,
	); err != nil {
		return fmt.Errorf("create account_id index: %w", err)
	}

	if _, err := db.Exec(
		`UPDATE subscription_key SET account_id = ? WHERE account_id IS NULL`,
		legacyAccountID,
	); err != nil {
		return fmt.Errorf("backfill legacy account: %w", err)
	}

	// Rebuild when we either just added the column (it's NULLable by ALTER TABLE
	// semantics — no default, can't be added as NOT NULL) or the column existed
	// but was NULLable. Skip when the column already had NOT NULL (fresh install
	// from the schema string).
	if !colExists || !colNotNull {
		if err := rebuildSubscriptionKeyNotNull(db); err != nil {
			return err
		}
	}

	return nil
}

// subscriptionKeyAccountIDColumn returns whether the account_id column exists
// on subscription_key and whether it carries a NOT NULL constraint.
func subscriptionKeyAccountIDColumn(db *sql.DB) (exists, notNull bool, err error) {
	rows, err := db.Query(`PRAGMA table_info(subscription_key)`)
	if err != nil {
		return false, false, fmt.Errorf("table_info: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid       int
			name      string
			typ       string
			nn        int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &typ, &nn, &dfltValue, &pk); err != nil {
			return false, false, fmt.Errorf("scan table_info: %w", err)
		}
		if name == "account_id" {
			return true, nn == 1, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, false, fmt.Errorf("table_info rows: %w", err)
	}
	return false, false, nil
}

// rebuildSubscriptionKeyNotNull rebuilds subscription_key with NOT NULL on
// account_id. PRAGMA foreign_keys must be toggled off around the rebuild
// because cross-table FK references would otherwise reject the rename step.
// The PRAGMA cannot be set inside a transaction.
//
// The entire sequence (pragma, transaction, pragma restore) runs on a single
// pinned *sql.Conn because PRAGMA foreign_keys is per-connection; with the
// default *sql.DB pool a subsequent statement could land on a different
// connection where FK enforcement is still ON.
//
// Recovery: a leftover subscription_key_old from a prior crashed run (e.g.
// fs-level interruption between rename and drop) is dropped up front so the
// rebuild can re-run idempotently.
func rebuildSubscriptionKeyNotNull(db *sql.DB) (err error) {
	ctx := context.Background()

	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire conn: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("disable fk: %w", err)
	}
	// Re-enable foreign keys on the same connection. If the re-enable itself
	// fails and the primary work succeeded, surface that error — silently
	// returning would leave FK enforcement disabled for the lifetime of this
	// connection (which is the only connection when MaxOpenConns=1).
	defer func() {
		if _, reEnableErr := conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`); reEnableErr != nil && err == nil {
			err = fmt.Errorf("re-enable fk: %w", reEnableErr)
		}
	}()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin rebuild tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	stmts := []string{
		`DROP TABLE IF EXISTS subscription_key_old`,
		`ALTER TABLE subscription_key RENAME TO subscription_key_old`,
		`CREATE TABLE subscription_key (
			id          TEXT PRIMARY KEY,
			component   TEXT NOT NULL,
			label       TEXT NOT NULL,
			active      INTEGER NOT NULL DEFAULT 1,
			created_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			expires_at  DATETIME,
			usage_count INTEGER NOT NULL DEFAULT 0,
			account_id  TEXT NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT
		)`,
		`INSERT INTO subscription_key (id, component, label, active, created_at, expires_at, usage_count, account_id)
		 SELECT id, component, label, active, created_at, expires_at, usage_count, account_id
		 FROM subscription_key_old`,
		`DROP TABLE subscription_key_old`,
		`CREATE INDEX IF NOT EXISTS idx_subscription_key_component ON subscription_key(component)`,
		`CREATE INDEX IF NOT EXISTS idx_subscription_key_account_id ON subscription_key(account_id)`,
	}
	for _, s := range stmts {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("rebuild subscription_key: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit rebuild: %w", err)
	}
	return nil
}

// Close releases the database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// CreateKey generates a new subscription key under the given account and
// persists it. It is an alias for CreateKeyForAccount kept on KeyStore so the
// keys handler can take a single store dependency.
func (s *SQLiteStore) CreateKey(ctx context.Context, accountID, component, label string, expiresAt *time.Time) (*Key, error) {
	return s.CreateKeyForAccount(ctx, accountID, component, label, expiresAt)
}

// GetByValue retrieves a key by its value (id column).
// Returns ErrNotFound if the key does not exist, ErrRevoked if active=0.
func (s *SQLiteStore) GetByValue(ctx context.Context, value string) (*Key, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, component, label, active, created_at, expires_at, usage_count, account_id
		 FROM subscription_key WHERE id = ?`, value)

	k, err := scanKey(row)
	if errors.Is(err, sql.ErrNoRows) {
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
		`SELECT id, component, label, active, created_at, expires_at, usage_count, account_id
		 FROM subscription_key WHERE id = ?`, id)

	k, err := scanKey(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("get key: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("get key: %w", err)
	}

	return k, nil // no Active check — returns revoked keys too
}

// ListKeys returns all keys, optionally filtered by component and/or account.
// Empty filter strings disable the corresponding filter; both may be combined.
// offset/limit follow D23 (caller clamps; the store passes through).
func (s *SQLiteStore) ListKeys(ctx context.Context, componentFilter, accountFilter string, offset, limit int) ([]*Key, error) {
	const cols = `id, component, label, active, created_at, expires_at, usage_count, account_id`
	query := `SELECT ` + cols + ` FROM subscription_key`
	args := make([]any, 0, 4)
	clauses := make([]string, 0, 2)
	if componentFilter != "" {
		clauses = append(clauses, `component = ?`)
		args = append(args, componentFilter)
	}
	if accountFilter != "" {
		clauses = append(clauses, `account_id = ?`)
		args = append(args, accountFilter)
	}
	if len(clauses) > 0 {
		query += ` WHERE ` + strings.Join(clauses, ` AND `)
	}
	query += ` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
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
	err := s.Scan(&k.ID, &k.Component, &k.Label, &activeInt, &createdStr, &expiresStr, &k.UsageCount, &k.AccountID)
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
	if errors.Is(err, sql.ErrNoRows) {
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
	if errors.Is(err, sql.ErrNoRows) {
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
