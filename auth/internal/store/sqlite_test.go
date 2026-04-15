package store

import (
	"context"
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

	k, err := s.CreateKey(ctx, "core", "test label", nil)
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

	k, err := s.CreateKey(ctx, "minion", "revoke test", nil)
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

	k, err := s.CreateKey(ctx, "sentinel", "usage test", nil)
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

	_, err := s.CreateKey(ctx, "core", "core key 1", nil)
	if err != nil {
		t.Fatalf("CreateKey core 1: %v", err)
	}
	_, err = s.CreateKey(ctx, "core", "core key 2", nil)
	if err != nil {
		t.Fatalf("CreateKey core 2: %v", err)
	}
	_, err = s.CreateKey(ctx, "minion", "minion key 1", nil)
	if err != nil {
		t.Fatalf("CreateKey minion: %v", err)
	}

	// All keys
	all, err := s.ListKeys(ctx, "")
	if err != nil {
		t.Fatalf("ListKeys all: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 total keys, got %d", len(all))
	}

	// Filter by component
	coreKeys, err := s.ListKeys(ctx, "core")
	if err != nil {
		t.Fatalf("ListKeys core: %v", err)
	}
	if len(coreKeys) != 2 {
		t.Errorf("expected 2 core keys, got %d", len(coreKeys))
	}

	minionKeys, err := s.ListKeys(ctx, "minion")
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
	comps, err := s.ListComponents(context.Background())
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

	comps, err := s.ListComponents(ctx)
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
		if _, err := s.CreateKey(ctx, "core", "core key", nil); err != nil {
			t.Fatalf("CreateKey core: %v", err)
		}
	}
	if _, err := s.CreateKey(ctx, "minion", "minion key", nil); err != nil {
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
	minionKeys, err := s.ListKeys(ctx, "minion")
	if err != nil {
		t.Fatalf("ListKeys minion: %v", err)
	}
	if len(minionKeys) != 1 || !minionKeys[0].Active {
		t.Error("minion key should still be active")
	}

	// Core keys should be revoked
	coreKeys, err := s.ListKeys(ctx, "core")
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
