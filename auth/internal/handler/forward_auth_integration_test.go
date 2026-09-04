/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

package handler

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/no42-org/packyard-auth/internal/store"
)

// newIntegrationStore opens an in-memory SQLiteStore and registers cleanup.
// This mirrors newTestStore in package store, which is not importable from here.
func newIntegrationStore(t *testing.T) *store.SQLiteStore {
	t.Helper()
	s, err := store.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("newIntegrationStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestForwardAuthIntegration_PrivateToPublic provisions a component as private,
// asserts unauthenticated access is denied (401), patches visibility to public,
// then asserts the same request is now allowed (200) — without a restart.
func TestForwardAuthIntegration_PrivateToPublic(t *testing.T) {
	s := newIntegrationStore(t)
	logger := slog.Default()

	// Wire both handlers to the same store instance.
	compHandler := NewComponentsHandler(s, logger, t.TempDir())
	authHandler := NewForwardAuthHandler(s, s, s, logger)

	// --- Step 1: Provision "core" as private via ComponentsHandler ---
	createBody, _ := json.Marshal(createComponentRequest{
		Name:             "core",
		Visibility:       "private",
		RPMSeries:        []string{},
		RPMOSFamilies:    []string{},
		RPMArchitectures: []string{},
	})
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/components", bytes.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	compHandler.Create(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create component: want 201, got %d — body: %s", createRec.Code, createRec.Body.String())
	}

	// --- Step 2: Unauthenticated forward-auth request → must return 401 ---
	authReq := httptest.NewRequest(http.MethodGet, "/auth", nil)
	authReq.Header.Set("X-Forwarded-Uri", "/rpm/core/2025/el9-x86_64/")
	authRec := httptest.NewRecorder()
	authHandler.ServeHTTP(authRec, authReq)
	if authRec.Code != http.StatusUnauthorized {
		t.Errorf("before patch (private): want 401, got %d", authRec.Code)
	}

	// --- Step 3: PATCH visibility to "public" via ComponentsHandler ---
	patchBody, _ := json.Marshal(updateComponentRequest{Visibility: "public"})
	patchReq := chiRequest(http.MethodPatch, "/api/v1/components/core", "core", patchBody)
	patchRec := httptest.NewRecorder()
	compHandler.Update(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch component: want 200, got %d — body: %s", patchRec.Code, patchRec.Body.String())
	}

	// --- Step 4: Same unauthenticated request → must now return 200 ---
	authReq2 := httptest.NewRequest(http.MethodGet, "/auth", nil)
	authReq2.Header.Set("X-Forwarded-Uri", "/rpm/core/2025/el9-x86_64/")
	authRec2 := httptest.NewRecorder()
	authHandler.ServeHTTP(authRec2, authReq2)
	if authRec2.Code != http.StatusOK {
		t.Errorf("after patch to public: want 200, got %d", authRec2.Code)
	}
}

// TestForwardAuthIntegration_PublicToPrivate provisions a component as public,
// asserts unauthenticated access is allowed (200), patches visibility to private,
// then asserts the same request is denied (401) — without a restart.
func TestForwardAuthIntegration_PublicToPrivate(t *testing.T) {
	s := newIntegrationStore(t)
	logger := slog.Default()

	// Wire both handlers to the same store instance.
	compHandler := NewComponentsHandler(s, logger, t.TempDir())
	authHandler := NewForwardAuthHandler(s, s, s, logger)

	// --- Step 1: Provision "core" as public via ComponentsHandler ---
	createBody, _ := json.Marshal(createComponentRequest{
		Name:             "core",
		Visibility:       "public",
		RPMSeries:        []string{},
		RPMOSFamilies:    []string{},
		RPMArchitectures: []string{},
	})
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/components", bytes.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	compHandler.Create(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create component: want 201, got %d — body: %s", createRec.Code, createRec.Body.String())
	}

	// --- Step 2: Unauthenticated forward-auth request → must return 200 ---
	authReq := httptest.NewRequest(http.MethodGet, "/auth", nil)
	authReq.Header.Set("X-Forwarded-Uri", "/rpm/core/2025/el9-x86_64/")
	authRec := httptest.NewRecorder()
	authHandler.ServeHTTP(authRec, authReq)
	if authRec.Code != http.StatusOK {
		t.Errorf("before patch (public): want 200, got %d", authRec.Code)
	}

	// --- Step 3: PATCH visibility to "private" via ComponentsHandler ---
	patchBody, _ := json.Marshal(updateComponentRequest{Visibility: "private"})
	patchReq := chiRequest(http.MethodPatch, "/api/v1/components/core", "core", patchBody)
	patchRec := httptest.NewRecorder()
	compHandler.Update(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch component: want 200, got %d — body: %s", patchRec.Code, patchRec.Body.String())
	}

	// --- Step 4: Same unauthenticated request → must now return 401 ---
	authReq2 := httptest.NewRequest(http.MethodGet, "/auth", nil)
	authReq2.Header.Set("X-Forwarded-Uri", "/rpm/core/2025/el9-x86_64/")
	authRec2 := httptest.NewRecorder()
	authHandler.ServeHTTP(authRec2, authReq2)
	if authRec2.Code != http.StatusUnauthorized {
		t.Errorf("after patch to private: want 401, got %d", authRec2.Code)
	}
}

// TestForwardAuthIntegration_DeletedComponent verifies that deleting a component
// causes forward-auth to return 401 immediately — the live DB lookup means the
// deletion takes effect on the very next request without a restart.
func TestForwardAuthIntegration_DeletedComponent(t *testing.T) {
	s := newIntegrationStore(t)
	logger := slog.Default()

	compHandler := NewComponentsHandler(s, logger, t.TempDir())
	authHandler := NewForwardAuthHandler(s, s, s, logger)

	// --- Step 1: Provision "core" as public ---
	createBody, _ := json.Marshal(createComponentRequest{
		Name:             "core",
		Visibility:       "public",
		RPMSeries:        []string{},
		RPMOSFamilies:    []string{},
		RPMArchitectures: []string{},
	})
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/components", bytes.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	compHandler.Create(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create component: want 201, got %d — body: %s", createRec.Code, createRec.Body.String())
	}

	// --- Step 2: Unauthenticated request → 200 (public) ---
	authReq := httptest.NewRequest(http.MethodGet, "/auth", nil)
	authReq.Header.Set("X-Forwarded-Uri", "/rpm/core/2025/el9-x86_64/")
	authRec := httptest.NewRecorder()
	authHandler.ServeHTTP(authRec, authReq)
	if authRec.Code != http.StatusOK {
		t.Errorf("before delete (public): want 200, got %d", authRec.Code)
	}

	// --- Step 3: Delete the component (with confirm) ---
	deleteReq := chiRequest(http.MethodDelete, "/api/v1/components/core?confirm=core", "core", nil)
	deleteRec := httptest.NewRecorder()
	compHandler.Delete(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete component: want 200, got %d — body: %s", deleteRec.Code, deleteRec.Body.String())
	}

	// --- Step 4: Same request → 401 immediately (component no longer in DB) ---
	authReq2 := httptest.NewRequest(http.MethodGet, "/auth", nil)
	authReq2.Header.Set("X-Forwarded-Uri", "/rpm/core/2025/el9-x86_64/")
	authRec2 := httptest.NewRecorder()
	authHandler.ServeHTTP(authRec2, authReq2)
	if authRec2.Code != http.StatusUnauthorized {
		t.Errorf("after delete: want 401, got %d", authRec2.Code)
	}
}
