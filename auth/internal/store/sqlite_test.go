/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestGetByValue_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetByValue(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}

func TestCreateKey_Success(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	k, err := s.CreateKey(ctx, legacyAccountID, "core", "test label", nil)
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	if len(k.ID) != 64 {
		t.Errorf("expected 64-char key ID, got %d chars: %s", len(k.ID), k.ID)
	}
	if k.Component != "core" {
		t.Errorf("expected component 'core', got %q", k.Component)
	}
	if k.Label != "test label" {
		t.Errorf("expected label 'test label', got %q", k.Label)
	}
	if !k.Active {
		t.Error("expected active=true")
	}
	if k.UsageCount != 0 {
		t.Errorf("expected usage_count=0, got %d", k.UsageCount)
	}
	if k.ExpiresAt != nil {
		t.Errorf("expected nil expires_at, got %v", k.ExpiresAt)
	}

	// Read back and verify
	got, err := s.GetByValue(ctx, k.ID)
	if err != nil {
		t.Fatalf("GetByValue: %v", err)
	}
	if got.ID != k.ID {
		t.Errorf("ID mismatch: want %q, got %q", k.ID, got.ID)
	}
	if got.Component != k.Component {
		t.Errorf("component mismatch: want %q, got %q", k.Component, got.Component)
	}
}

func TestRevokeKey(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	k, err := s.CreateKey(ctx, legacyAccountID, "minion", "revoke test", nil)
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	if err := s.RevokeKey(ctx, k.ID); err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}

	_, err = s.GetByValue(ctx, k.ID)
	if err == nil {
		t.Fatal("expected error after revoke, got nil")
	}
	if !errors.Is(err, ErrRevoked) {
		t.Fatalf("expected ErrRevoked, got: %v", err)
	}
}

func TestIncrementUsage(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	k, err := s.CreateKey(ctx, legacyAccountID, "sentinel", "usage test", nil)
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := s.IncrementUsage(ctx, k.ID); err != nil {
			t.Fatalf("IncrementUsage iteration %d: %v", i, err)
		}
	}

	got, err := s.GetByValue(ctx, k.ID)
	if err != nil {
		t.Fatalf("GetByValue: %v", err)
	}
	if got.UsageCount != 3 {
		t.Errorf("expected usage_count=3, got %d", got.UsageCount)
	}
}

func TestListKeys(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, err := s.CreateKey(ctx, legacyAccountID, "core", "core key 1", nil)
	if err != nil {
		t.Fatalf("CreateKey core 1: %v", err)
	}
	_, err = s.CreateKey(ctx, legacyAccountID, "core", "core key 2", nil)
	if err != nil {
		t.Fatalf("CreateKey core 2: %v", err)
	}
	_, err = s.CreateKey(ctx, legacyAccountID, "minion", "minion key 1", nil)
	if err != nil {
		t.Fatalf("CreateKey minion: %v", err)
	}

	// All keys
	all, err := s.ListKeys(ctx, "", "", 0, 50)
	if err != nil {
		t.Fatalf("ListKeys all: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 total keys, got %d", len(all))
	}

	// Filter by component
	coreKeys, err := s.ListKeys(ctx, "core", "", 0, 50)
	if err != nil {
		t.Fatalf("ListKeys core: %v", err)
	}
	if len(coreKeys) != 2 {
		t.Errorf("expected 2 core keys, got %d", len(coreKeys))
	}

	minionKeys, err := s.ListKeys(ctx, "minion", "", 0, 50)
	if err != nil {
		t.Fatalf("ListKeys minion: %v", err)
	}
	if len(minionKeys) != 1 {
		t.Errorf("expected 1 minion key, got %d", len(minionKeys))
	}
}

// ─── Component store tests ────────────────────────────────────────────────────

func newTestComponent(name, vis string) *Component {
	return &Component{
		Name:             name,
		Visibility:       vis,
		RPMSeries:        []string{"2025"},
		RPMOSFamilies:    []string{"el9"},
		RPMArchitectures: []string{"x86_64"},
	}
}

func TestCreateComponent_Success(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	comp, err := s.CreateComponent(ctx, newTestComponent("core", "private"))
	if err != nil {
		t.Fatalf("CreateComponent: %v", err)
	}
	if comp.Name != "core" {
		t.Errorf("name: want core, got %q", comp.Name)
	}
	if comp.Visibility != "private" {
		t.Errorf("visibility: want private, got %q", comp.Visibility)
	}
	if len(comp.RPMSeries) != 1 || comp.RPMSeries[0] != "2025" {
		t.Errorf("rpm_series: got %v", comp.RPMSeries)
	}
}

func TestCreateComponent_DefaultVisibility(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	comp, err := s.CreateComponent(ctx, &Component{
		Name:             "core",
		RPMSeries:        []string{"2025"},
		RPMOSFamilies:    []string{"el9"},
		RPMArchitectures: []string{"x86_64"},
	})
	if err != nil {
		t.Fatalf("CreateComponent: %v", err)
	}
	if comp.Visibility != "private" {
		t.Errorf("expected default visibility 'private', got %q", comp.Visibility)
	}
}

func TestCreateComponent_DuplicateReturnsExists(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.CreateComponent(ctx, newTestComponent("core", "private")); err != nil {
		t.Fatalf("first CreateComponent: %v", err)
	}
	_, err := s.CreateComponent(ctx, newTestComponent("core", "private"))
	if err == nil {
		t.Fatal("expected ErrComponentExists, got nil")
	}
	if !errors.Is(err, ErrComponentExists) {
		t.Fatalf("expected ErrComponentExists, got: %v", err)
	}
}

func TestGetComponent_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetComponent(context.Background(), "unknown")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrComponentNotFound) {
		t.Fatalf("expected ErrComponentNotFound, got: %v", err)
	}
}

func TestGetComponent_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	created, err := s.CreateComponent(ctx, &Component{
		Name:             "minion",
		Visibility:       "public",
		RPMSeries:        []string{"2025", "2026"},
		RPMOSFamilies:    []string{"el9", "el10"},
		RPMArchitectures: []string{"x86_64", "aarch64"},
	})
	if err != nil {
		t.Fatalf("CreateComponent: %v", err)
	}

	got, err := s.GetComponent(ctx, "minion")
	if err != nil {
		t.Fatalf("GetComponent: %v", err)
	}
	if got.Name != created.Name {
		t.Errorf("name: want %q, got %q", created.Name, got.Name)
	}
	if got.Visibility != "public" {
		t.Errorf("visibility: want public, got %q", got.Visibility)
	}
	if len(got.RPMSeries) != 2 {
		t.Errorf("rpm_series count: want 2, got %d", len(got.RPMSeries))
	}
	if len(got.RPMArchitectures) != 2 {
		t.Errorf("rpm_architectures count: want 2, got %d", len(got.RPMArchitectures))
	}
}

func TestListComponents_Empty(t *testing.T) {
	s := newTestStore(t)
	comps, err := s.ListComponents(context.Background(), 0, 50)
	if err != nil {
		t.Fatalf("ListComponents: %v", err)
	}
	if len(comps) != 0 {
		t.Errorf("expected 0 components, got %d", len(comps))
	}
}

func TestListComponents_Ordered(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for _, name := range []string{"sentinel", "core", "minion"} {
		if _, err := s.CreateComponent(ctx, newTestComponent(name, "private")); err != nil {
			t.Fatalf("CreateComponent %s: %v", name, err)
		}
	}

	comps, err := s.ListComponents(ctx, 0, 50)
	if err != nil {
		t.Fatalf("ListComponents: %v", err)
	}
	if len(comps) != 3 {
		t.Fatalf("expected 3 components, got %d", len(comps))
	}
	// Should be alphabetically ordered
	if comps[0].Name != "core" || comps[1].Name != "minion" || comps[2].Name != "sentinel" {
		t.Errorf("unexpected order: %s, %s, %s", comps[0].Name, comps[1].Name, comps[2].Name)
	}
}

func TestDeleteComponent_Success(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.CreateComponent(ctx, newTestComponent("core", "private")); err != nil {
		t.Fatalf("CreateComponent: %v", err)
	}

	if err := s.DeleteComponent(ctx, "core"); err != nil {
		t.Fatalf("DeleteComponent: %v", err)
	}

	_, err := s.GetComponent(ctx, "core")
	if err == nil {
		t.Fatal("expected ErrComponentNotFound after delete, got nil")
	}
	if !errors.Is(err, ErrComponentNotFound) {
		t.Fatalf("expected ErrComponentNotFound, got: %v", err)
	}
}

func TestDeleteComponent_NotFound(t *testing.T) {
	s := newTestStore(t)
	err := s.DeleteComponent(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrComponentNotFound) {
		t.Fatalf("expected ErrComponentNotFound, got: %v", err)
	}
}

func TestRevokeComponentKeys(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Create keys for core and minion
	for i := 0; i < 3; i++ {
		if _, err := s.CreateKey(ctx, legacyAccountID, "core", "core key", nil); err != nil {
			t.Fatalf("CreateKey core: %v", err)
		}
	}
	if _, err := s.CreateKey(ctx, legacyAccountID, "minion", "minion key", nil); err != nil {
		t.Fatalf("CreateKey minion: %v", err)
	}

	revoked, err := s.RevokeComponentKeys(ctx, "core")
	if err != nil {
		t.Fatalf("RevokeComponentKeys: %v", err)
	}
	if revoked != 3 {
		t.Errorf("expected 3 keys revoked, got %d", revoked)
	}

	// Minion key should still be active
	minionKeys, err := s.ListKeys(ctx, "minion", "", 0, 50)
	if err != nil {
		t.Fatalf("ListKeys minion: %v", err)
	}
	if len(minionKeys) != 1 || !minionKeys[0].Active {
		t.Error("minion key should still be active")
	}

	// Core keys should be revoked
	coreKeys, err := s.ListKeys(ctx, "core", "", 0, 50)
	if err != nil {
		t.Fatalf("ListKeys core: %v", err)
	}
	for _, k := range coreKeys {
		if k.Active {
			t.Errorf("core key %s should be revoked", k.ID)
		}
	}
}

func TestLoadComponentSets(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Empty DB
	valid, public, err := s.LoadComponentSets(ctx)
	if err != nil {
		t.Fatalf("LoadComponentSets empty: %v", err)
	}
	if len(valid) != 0 || len(public) != 0 {
		t.Errorf("expected empty maps, got valid=%v public=%v", valid, public)
	}

	// Add components
	if _, err := s.CreateComponent(ctx, newTestComponent("core", "private")); err != nil {
		t.Fatalf("CreateComponent core: %v", err)
	}
	if _, err := s.CreateComponent(ctx, &Component{
		Name: "minion", Visibility: "public",
		RPMSeries: []string{"2025"}, RPMOSFamilies: []string{"el9"}, RPMArchitectures: []string{"x86_64"},
	}); err != nil {
		t.Fatalf("CreateComponent minion: %v", err)
	}

	valid, public, err = s.LoadComponentSets(ctx)
	if err != nil {
		t.Fatalf("LoadComponentSets: %v", err)
	}
	if !valid["core"] || !valid["minion"] {
		t.Errorf("expected both components valid, got %v", valid)
	}
	if public["core"] {
		t.Error("core should not be public")
	}
	if !public["minion"] {
		t.Error("minion should be public")
	}
}

func TestUpdateComponentVisibility_Success(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.CreateComponent(ctx, newTestComponent("core", "private")); err != nil {
		t.Fatalf("CreateComponent: %v", err)
	}

	updated, err := s.UpdateComponentVisibility(ctx, "core", "public")
	if err != nil {
		t.Fatalf("UpdateComponentVisibility: %v", err)
	}
	if updated.Visibility != "public" {
		t.Errorf("expected visibility public, got %q", updated.Visibility)
	}
	if updated.Name != "core" {
		t.Errorf("expected name core, got %q", updated.Name)
	}

	// Confirm the change is persisted.
	got, err := s.GetComponent(ctx, "core")
	if err != nil {
		t.Fatalf("GetComponent after update: %v", err)
	}
	if got.Visibility != "public" {
		t.Errorf("persisted visibility: want public, got %q", got.Visibility)
	}
}

func TestUpdateComponentVisibility_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.UpdateComponentVisibility(context.Background(), "nonexistent", "public")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrComponentNotFound) {
		t.Fatalf("expected ErrComponentNotFound, got: %v", err)
	}
}

func TestUpdateComponentVisibility_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.CreateComponent(ctx, newTestComponent("core", "public")); err != nil {
		t.Fatalf("CreateComponent: %v", err)
	}

	// public → private → public
	for _, vis := range []string{"private", "public"} {
		got, err := s.UpdateComponentVisibility(ctx, "core", vis)
		if err != nil {
			t.Fatalf("UpdateComponentVisibility(%q): %v", vis, err)
		}
		if got.Visibility != vis {
			t.Errorf("returned visibility: want %q, got %q", vis, got.Visibility)
		}
	}
}

// ─── Migration + FK tests (change 2026-05-21-admin-ui-account-lifecycle § 1) ──

// TestMigration_LegacyAccountSeeded verifies the synthetic 'legacy' account
// exists after store initialisation (task 1.5).
func TestMigration_LegacyAccountSeeded(t *testing.T) {
	s := newTestStore(t)

	var (
		id, email, status, createdBy string
	)
	err := s.db.QueryRow(
		`SELECT id, email, status, created_by_operator_id FROM accounts WHERE id = ?`,
		legacyAccountID,
	).Scan(&id, &email, &status, &createdBy)
	if err != nil {
		t.Fatalf("legacy account row: %v", err)
	}
	if id != "legacy" || email != "legacy@packyard.internal" || status != "active" || createdBy != "bootstrap" {
		t.Errorf("legacy account row mismatch: id=%q email=%q status=%q createdBy=%q",
			id, email, status, createdBy)
	}
}

// TestMigration_Idempotent verifies that re-opening the store does not corrupt
// or duplicate the schema (task 1.5/1.8 idempotency).
func TestMigration_Idempotent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	k, err := s.CreateKey(ctx, legacyAccountID, "core", "pre-reopen", nil)
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	// Re-running NewSQLiteStore on the same underlying *sql.DB is not possible,
	// but the schema string + migration steps must be idempotent against an
	// already-migrated database. Re-execute them and verify nothing breaks.
	if _, err := s.db.Exec(schema); err != nil {
		t.Fatalf("re-exec schema: %v", err)
	}
	if err := seedLegacyAccount(s.db); err != nil {
		t.Fatalf("re-seed: %v", err)
	}
	if err := migrateSubscriptionKeyAccountID(s.db); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}

	got, err := s.GetByValue(ctx, k.ID)
	if err != nil {
		t.Fatalf("GetByValue after re-migrate: %v", err)
	}
	if got.AccountID != "legacy" {
		t.Errorf("AccountID after re-migrate: want legacy, got %q", got.AccountID)
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM accounts WHERE id = ?`, legacyAccountID).Scan(&count); err != nil {
		t.Fatalf("count legacy: %v", err)
	}
	if count != 1 {
		t.Errorf("legacy account count: want 1, got %d", count)
	}
}

// TestMigration_NotNullEnforced verifies that account_id is NOT NULL on the
// final subscription_key schema (task 1.8). The PRAGMA reflects the rebuilt
// table for existing installs and the fresh-install table for new ones.
func TestMigration_NotNullEnforced(t *testing.T) {
	s := newTestStore(t)

	exists, notNull, err := subscriptionKeyAccountIDColumn(s.db)
	if err != nil {
		t.Fatalf("introspect account_id: %v", err)
	}
	if !exists {
		t.Fatal("subscription_key.account_id missing")
	}
	if !notNull {
		t.Fatal("subscription_key.account_id is NULLable; expected NOT NULL after migration")
	}
}

// TestMigration_LegacyBackfill simulates the existing-install path: build a
// subscription_key without account_id, insert legacy rows, then run the
// migration. Rows must be backfilled to 'legacy' and the column must be NOT NULL.
func TestMigration_LegacyBackfill(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	for _, p := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := db.Exec(p); err != nil {
			t.Fatalf("pragma %s: %v", p, err)
		}
	}

	// Pre-account-lifecycle schema: subscription_key without account_id.
	if _, err := db.Exec(`
		CREATE TABLE subscription_key (
			id          TEXT PRIMARY KEY,
			component   TEXT NOT NULL,
			label       TEXT NOT NULL,
			active      INTEGER NOT NULL DEFAULT 1,
			created_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			expires_at  DATETIME,
			usage_count INTEGER NOT NULL DEFAULT 0
		)
	`); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO subscription_key (id, component, label, created_at)
		VALUES ('keyA', 'core', 'A', '2026-01-01T00:00:00Z'),
		       ('keyB', 'minion', 'B', '2026-01-02T00:00:00Z')
	`); err != nil {
		t.Fatalf("seed legacy rows: %v", err)
	}

	// Roll forward.
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	if err := seedLegacyAccount(db); err != nil {
		t.Fatalf("seed legacy account: %v", err)
	}
	if err := migrateSubscriptionKeyAccountID(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	rows, err := db.Query(`SELECT id, account_id FROM subscription_key ORDER BY id`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var id, accountID string
		if err := rows.Scan(&id, &accountID); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[id] = accountID
	}
	if got["keyA"] != "legacy" || got["keyB"] != "legacy" {
		t.Errorf("backfill: want both rows -> legacy, got %v", got)
	}

	_, notNull, err := subscriptionKeyAccountIDColumn(db)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	if !notNull {
		t.Fatal("account_id is NULLable after rebuild; expected NOT NULL")
	}

	// Second run must be a no-op: the rebuild branch should not re-execute,
	// row count and account_id values stay put, and the column stays NOT NULL.
	if err := migrateSubscriptionKeyAccountID(db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	var rowCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM subscription_key`).Scan(&rowCount); err != nil {
		t.Fatalf("count after second migrate: %v", err)
	}
	if rowCount != 2 {
		t.Errorf("row count after second migrate: want 2, got %d", rowCount)
	}
	_, notNull2, err := subscriptionKeyAccountIDColumn(db)
	if err != nil {
		t.Fatalf("introspect after second migrate: %v", err)
	}
	if !notNull2 {
		t.Fatal("account_id became NULLable after second migrate")
	}
}

// TestKeysAccountIDRestrict verifies the FK ON DELETE RESTRICT on
// subscription_key.account_id (D20): deleting an account that still owns keys
// must fail.
func TestKeysAccountIDRestrict(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.CreateKey(ctx, legacyAccountID, "core", "legacy-owned", nil); err != nil {
		t.Fatalf("CreateKey: %v", err)
	}

	_, err := s.db.ExecContext(ctx, `DELETE FROM accounts WHERE id = ?`, legacyAccountID)
	if err == nil {
		t.Fatal("expected FK RESTRICT error deleting account with active keys, got nil")
	}
}

// TestSessionsOperatorCascade verifies the FK ON DELETE CASCADE on
// sessions.operator_id (D16): deleting an operator must also delete their
// sessions.
func TestSessionsOperatorCascade(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO operators (id, email, role, status, allowlisted_at)
		VALUES ('op1', 'a@example.com', 'admin', 'active', '2026-01-01T00:00:00Z')
	`); err != nil {
		t.Fatalf("seed operator: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (id, operator_id, created_at, last_seen_at, expires_at)
		VALUES ('sess1', 'op1', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-02T00:00:00Z')
	`); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	if _, err := s.db.ExecContext(ctx, `DELETE FROM operators WHERE id = ?`, "op1"); err != nil {
		t.Fatalf("delete operator: %v", err)
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = ?`, "sess1").Scan(&count); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if count != 0 {
		t.Errorf("expected session cascade-delete, got %d remaining", count)
	}
}

// ─── Account store tests (change § 2.1) ──────────────────────────────────────

func TestAccountStore_CreateAndGet(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	a, err := s.CreateAccount(ctx, AccountInput{Email: "Customer@Example.COM", OrgName: "Acme"}, "operator-1")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if a.Email != "customer@example.com" {
		t.Errorf("expected canonicalised email, got %q", a.Email)
	}
	if a.Status != AccountStatusActive {
		t.Errorf("expected default status active, got %q", a.Status)
	}
	if len(a.ID) != 32 {
		t.Errorf("expected 32-char id, got %d", len(a.ID))
	}

	got, err := s.GetAccount(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if got.Email != a.Email || got.OrgName != "Acme" || got.CreatedByOperatorID != "operator-1" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestAccountStore_EmailUniquenessCaseInsensitive(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.CreateAccount(ctx, AccountInput{Email: "customer@example.com"}, "op"); err != nil {
		t.Fatalf("first CreateAccount: %v", err)
	}
	_, err := s.CreateAccount(ctx, AccountInput{Email: "Customer@Example.COM"}, "op")
	if !errors.Is(err, ErrAccountEmailExists) {
		t.Fatalf("expected ErrAccountEmailExists, got: %v", err)
	}
}

func TestAccountStore_EmailCheckRejectsEmpty(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, err := s.CreateAccount(ctx, AccountInput{Email: ""}, "op")
	if err == nil {
		t.Fatal("expected CHECK constraint error for empty email, got nil")
	}
}

func TestAccountStore_DeletedAccountInvisible(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	a, err := s.CreateAccount(ctx, AccountInput{Email: "x@y.com"}, "op")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if _, err := s.DeleteAccountWithCascade(ctx, a.ID); err != nil {
		t.Fatalf("DeleteAccountWithCascade: %v", err)
	}
	_, err = s.GetAccount(ctx, a.ID)
	if !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("expected ErrAccountNotFound for deleted account, got: %v", err)
	}
}

func TestAccountStore_DeleteCascadesKeyRevocation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	a, err := s.CreateAccount(ctx, AccountInput{Email: "deleter@x.com"}, "op")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.CreateKeyForAccount(ctx, a.ID, "core", "k", nil); err != nil {
			t.Fatalf("CreateKeyForAccount %d: %v", i, err)
		}
	}
	// One already-revoked key to verify it isn't double-counted.
	revoked, err := s.CreateKeyForAccount(ctx, a.ID, "core", "pre-revoked", nil)
	if err != nil {
		t.Fatalf("CreateKeyForAccount revoked: %v", err)
	}
	if err := s.RevokeKey(ctx, revoked.ID); err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}

	n, err := s.DeleteAccountWithCascade(ctx, a.ID)
	if err != nil {
		t.Fatalf("DeleteAccountWithCascade: %v", err)
	}
	if n != 3 {
		t.Errorf("expected 3 keys revoked, got %d", n)
	}

	// All 4 keys must be active=0 now.
	keys, err := s.ListAccountKeys(ctx, a.ID, 0, 50)
	if err != nil {
		t.Fatalf("ListAccountKeys: %v", err)
	}
	if len(keys) != 4 {
		t.Fatalf("expected 4 keys total, got %d", len(keys))
	}
	for _, k := range keys {
		if k.Active {
			t.Errorf("key %s still active after cascade", k.ID)
		}
	}
}

func TestAccountStore_SuspendKeepsKeysActive(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	a, err := s.CreateAccount(ctx, AccountInput{Email: "suspendme@x.com"}, "op")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	k, err := s.CreateKeyForAccount(ctx, a.ID, "core", "k", nil)
	if err != nil {
		t.Fatalf("CreateKeyForAccount: %v", err)
	}

	suspended := AccountStatusSuspended
	if _, err := s.UpdateAccount(ctx, a.ID, AccountUpdate{Status: &suspended}); err != nil {
		t.Fatalf("UpdateAccount suspend: %v", err)
	}
	got, err := s.GetByID(ctx, k.ID)
	if err != nil {
		t.Fatalf("GetByID after suspend: %v", err)
	}
	if !got.Active {
		t.Errorf("key was modified during suspend; expected active=true")
	}

	active := AccountStatusActive
	if _, err := s.UpdateAccount(ctx, a.ID, AccountUpdate{Status: &active}); err != nil {
		t.Fatalf("UpdateAccount reactivate: %v", err)
	}
	got, err = s.GetByID(ctx, k.ID)
	if err != nil {
		t.Fatalf("GetByID after reactivate: %v", err)
	}
	if !got.Active {
		t.Errorf("key changed during reactivate; expected active=true")
	}
}

func TestAccountStore_CreateKeyForUnknownAccount(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_, err := s.CreateKeyForAccount(ctx, "nonexistent", "core", "k", nil)
	if !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("expected ErrAccountNotFound, got: %v", err)
	}
}

func TestAccountStore_ListAccountsExcludesDeleted(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	a1, _ := s.CreateAccount(ctx, AccountInput{Email: "a@x.com"}, "op")
	_, _ = s.CreateAccount(ctx, AccountInput{Email: "b@x.com"}, "op")
	_, _ = s.DeleteAccountWithCascade(ctx, a1.ID)

	// Two non-deleted: legacy (seed) + b. a1 was soft-deleted.
	all, err := s.ListAccounts(ctx, "", 0, 50)
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("expected 2 non-deleted accounts (legacy + b), got %d", len(all))
	}

	deleted, err := s.ListAccounts(ctx, AccountStatusDeleted, 0, 50)
	if err != nil {
		t.Fatalf("ListAccounts deleted: %v", err)
	}
	if len(deleted) != 1 {
		t.Errorf("expected 1 deleted account when explicitly filtered, got %d", len(deleted))
	}
}

// TestCanonicalEmail covers the lowercase + trim canonicalisation contract
// (D19). Whitespace trimming was added after § 5 review surfaced that the
// operators/accounts CHECK constraint (email LIKE '%_@_%') admits padded
// inputs, allowing an allowlist row the OAuth canonical lookup could never
// match.
func TestCanonicalEmail(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Alice@Example.com", "alice@example.com"},
		{"BOB@EXAMPLE.COM", "bob@example.com"},
		{"already@lower.com", "already@lower.com"},
		{"with.dots+plus@example.com", "with.dots+plus@example.com"},         // no dot/plus normalisation per D19
		{"  Alice@Example.com  ", "alice@example.com"},                       // trim + lowercase
		{"\t bob@example.com \n", "bob@example.com"},                         // trim mixed whitespace
		{"no-trim-inside.com@example.com", "no-trim-inside.com@example.com"}, // interior whitespace would fail CHECK; this confirms no inner trim
	}
	for _, c := range cases {
		if got := canonicalEmail(c.in); got != c.want {
			t.Errorf("canonicalEmail(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
