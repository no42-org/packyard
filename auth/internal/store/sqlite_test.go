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
