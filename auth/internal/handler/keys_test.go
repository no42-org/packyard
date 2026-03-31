package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/opennms/packyard-auth/internal/store"
)

// testKeyID is a valid 64-char hex key ID used across inspect tests.
const testKeyID = "aabbccdd" + "aabbccdd" + "aabbccdd" + "aabbccdd" +
	"aabbccdd" + "aabbccdd" + "aabbccdd" + "aabbccdd"

func newTestKeysHandler(s store.KeyStore) *KeysHandler {
	return &KeysHandler{Store: s, Logger: slog.Default()}
}

// makeKey returns a minimal *store.Key for use in mockStore responses.
func makeKey(component, label string) *store.Key {
	return &store.Key{
		ID:         "aabbccdd" + "aabbccdd" + "aabbccdd" + "aabbccdd" + "aabbccdd" + "aabbccdd" + "aabbccdd" + "aabbccdd",
		Component:  component,
		Label:      label,
		Active:     true,
		CreatedAt:  time.Now().UTC(),
		ExpiresAt:  nil,
		UsageCount: 0,
	}
}

func postKeys(h *KeysHandler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/keys", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Create(w, req)
	return w
}

// TestCreate_ValidCore — AC1, AC3: valid core component returns 201 with Key JSON.
func TestCreate_ValidCore(t *testing.T) {
	h := newTestKeysHandler(&mockStore{
		createKeyFn: func(_ context.Context, component, label string, _ *time.Time) (*store.Key, error) {
			return makeKey(component, label), nil
		},
	})
	w := postKeys(h, `{"component":"core","label":"Acme Corp - Core","expires_at":null}`)

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d", w.Code)
	}
	var key store.Key
	if err := json.NewDecoder(w.Body).Decode(&key); err != nil {
		t.Fatalf("response not valid JSON: %v", err)
	}
	if key.Component != "core" {
		t.Errorf("expected component=core, got %q", key.Component)
	}
	if !key.Active {
		t.Errorf("expected active=true")
	}
	if key.UsageCount != 0 {
		t.Errorf("expected usage_count=0, got %d", key.UsageCount)
	}
}

// TestCreate_ValidMinion — AC3: minion component accepted.
func TestCreate_ValidMinion(t *testing.T) {
	h := newTestKeysHandler(&mockStore{
		createKeyFn: func(_ context.Context, component, label string, _ *time.Time) (*store.Key, error) {
			return makeKey(component, label), nil
		},
	})
	w := postKeys(h, `{"component":"minion","label":"Minion Sub"}`)

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d", w.Code)
	}
	var key store.Key
	if err := json.NewDecoder(w.Body).Decode(&key); err != nil {
		t.Fatalf("response not valid JSON: %v", err)
	}
	if key.Component != "minion" {
		t.Errorf("expected component=minion, got %q", key.Component)
	}
}

// TestCreate_ValidSentinel — AC3: sentinel component accepted.
func TestCreate_ValidSentinel(t *testing.T) {
	h := newTestKeysHandler(&mockStore{
		createKeyFn: func(_ context.Context, component, label string, _ *time.Time) (*store.Key, error) {
			return makeKey(component, label), nil
		},
	})
	w := postKeys(h, `{"component":"sentinel","label":"Sentinel Sub"}`)

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d", w.Code)
	}
}

// TestCreate_InvalidComponent — AC2: unknown component returns 400 INVALID_COMPONENT.
func TestCreate_InvalidComponent(t *testing.T) {
	h := newTestKeysHandler(&mockStore{
		createKeyFn: func(_ context.Context, _, _ string, _ *time.Time) (*store.Key, error) {
			t.Fatal("CreateKey must not be called for invalid component")
			return nil, nil
		},
	})
	w := postKeys(h, `{"component":"invalid","label":"test"}`)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
	var ae apiError
	if err := json.NewDecoder(w.Body).Decode(&ae); err != nil {
		t.Fatalf("response not valid JSON: %v", err)
	}
	if ae.Code != "INVALID_COMPONENT" {
		t.Errorf("expected code=INVALID_COMPONENT, got %q", ae.Code)
	}
	if ae.Message == "" {
		t.Errorf("expected non-empty message")
	}
}

// TestCreate_EmptyComponent — AC2: empty string is not a valid component.
func TestCreate_EmptyComponent(t *testing.T) {
	h := newTestKeysHandler(&mockStore{
		createKeyFn: func(_ context.Context, _, _ string, _ *time.Time) (*store.Key, error) {
			t.Fatal("CreateKey must not be called for empty component")
			return nil, nil
		},
	})
	w := postKeys(h, `{"component":"","label":"test"}`)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
	var ae apiError
	if err := json.NewDecoder(w.Body).Decode(&ae); err != nil {
		t.Fatalf("response not valid JSON: %v", err)
	}
	if ae.Code != "INVALID_COMPONENT" {
		t.Errorf("expected code=INVALID_COMPONENT, got %q", ae.Code)
	}
}

// TestCreate_LabelStored — AC4: label round-trips in the response.
func TestCreate_LabelStored(t *testing.T) {
	const wantLabel = "Acme Corporation — Core Subscription"
	h := newTestKeysHandler(&mockStore{
		createKeyFn: func(_ context.Context, component, label string, _ *time.Time) (*store.Key, error) {
			return makeKey(component, label), nil
		},
	})
	body, _ := json.Marshal(map[string]any{"component": "core", "label": wantLabel})
	w := postKeys(h, string(body))

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d", w.Code)
	}
	var key store.Key
	if err := json.NewDecoder(w.Body).Decode(&key); err != nil {
		t.Fatalf("response not valid JSON: %v", err)
	}
	if key.Label != wantLabel {
		t.Errorf("expected label=%q, got %q", wantLabel, key.Label)
	}
}

// TestCreate_StoreError — store failure returns 500 with empty body.
func TestCreate_StoreError(t *testing.T) {
	h := newTestKeysHandler(&mockStore{
		createKeyFn: func(_ context.Context, _, _ string, _ *time.Time) (*store.Key, error) {
			return nil, errors.New("database locked")
		},
	})
	w := postKeys(h, `{"component":"core","label":"test"}`)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("expected empty body on 500, got %q", w.Body.String())
	}
}

// getKeys issues GET /api/v1/keys with an optional query string (e.g. "?component=core").
func getKeys(h *KeysHandler, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/keys"+query, nil)
	w := httptest.NewRecorder()
	h.List(w, req)
	return w
}

// TestList_NoFilter — AC1: no filter returns all keys, 200.
func TestList_NoFilter(t *testing.T) {
	want := []*store.Key{makeKey("core", "A"), makeKey("minion", "B")}
	h := newTestKeysHandler(&mockStore{
		listKeysFn: func(_ context.Context, component string) ([]*store.Key, error) {
			if component != "" {
				t.Errorf("expected empty component, got %q", component)
			}
			return want, nil
		},
	})
	w := getKeys(h, "")

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var got []*store.Key
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("response not valid JSON: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 keys, got %d", len(got))
	}
}

// TestList_FilterCore — AC2: ?component=core passes filter to store, returns 200.
func TestList_FilterCore(t *testing.T) {
	h := newTestKeysHandler(&mockStore{
		listKeysFn: func(_ context.Context, component string) ([]*store.Key, error) {
			if component != "core" {
				t.Errorf("expected component=core, got %q", component)
			}
			return []*store.Key{makeKey("core", "Only Core")}, nil
		},
	})
	w := getKeys(h, "?component=core")

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var got []*store.Key
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("response not valid JSON: %v", err)
	}
	if len(got) != 1 || got[0].Component != "core" {
		t.Errorf("expected 1 core key, got %+v", got)
	}
}

// TestList_FilterInvalid — AC3: invalid component returns 400 INVALID_COMPONENT; store not called.
func TestList_FilterInvalid(t *testing.T) {
	h := newTestKeysHandler(&mockStore{
		listKeysFn: func(_ context.Context, _ string) ([]*store.Key, error) {
			t.Fatal("ListKeys must not be called for invalid component")
			return nil, nil
		},
	})
	w := getKeys(h, "?component=invalid")

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
	var ae apiError
	if err := json.NewDecoder(w.Body).Decode(&ae); err != nil {
		t.Fatalf("response not valid JSON: %v", err)
	}
	if ae.Code != "INVALID_COMPONENT" {
		t.Errorf("expected code=INVALID_COMPONENT, got %q", ae.Code)
	}
}

// TestList_Empty — AC4: nil slice from store encodes as [] not null.
func TestList_Empty(t *testing.T) {
	h := newTestKeysHandler(&mockStore{
		listKeysFn: func(_ context.Context, _ string) ([]*store.Key, error) {
			return nil, nil
		},
	})
	w := getKeys(h, "")

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := strings.TrimSpace(w.Body.String())
	if body != "[]" {
		t.Errorf("expected body=[], got %q", body)
	}
}

// TestList_IncludesRevoked — AC5: revoked keys (active=false) are included in the listing.
func TestList_IncludesRevoked(t *testing.T) {
	revoked := &store.Key{
		ID:        makeKey("core", "X").ID,
		Component: "core",
		Label:     "X",
		Active:    false,
		CreatedAt: time.Now().UTC(),
	}
	h := newTestKeysHandler(&mockStore{
		listKeysFn: func(_ context.Context, _ string) ([]*store.Key, error) {
			return []*store.Key{revoked}, nil
		},
	})
	w := getKeys(h, "")

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var got []*store.Key
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("response not valid JSON: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 key, got %d", len(got))
	}
	if got[0].Active {
		t.Errorf("expected active=false for revoked key")
	}
}

// TestList_StoreError — store failure returns 500 with empty body.
func TestList_StoreError(t *testing.T) {
	h := newTestKeysHandler(&mockStore{
		listKeysFn: func(_ context.Context, _ string) ([]*store.Key, error) {
			return nil, errors.New("database locked")
		},
	})
	w := getKeys(h, "")

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("expected empty body on 500, got %q", w.Body.String())
	}
}

// inspectKey issues GET /api/v1/keys/{id} with chi route context set.
func inspectKey(h *KeysHandler, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/keys/"+id, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	h.Get(w, req)
	return w
}

// TestGet_Exists — AC1: valid id returns 200 with full Key object.
func TestGet_Exists(t *testing.T) {
	want := makeKey("core", "Acme Corp")
	want.ID = testKeyID
	h := newTestKeysHandler(&mockStore{
		getByIDFn: func(_ context.Context, id string) (*store.Key, error) {
			if id != testKeyID {
				t.Errorf("unexpected id %q", id)
			}
			return want, nil
		},
	})
	w := inspectKey(h, testKeyID)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var key store.Key
	if err := json.NewDecoder(w.Body).Decode(&key); err != nil {
		t.Fatalf("response not valid JSON: %v", err)
	}
	if key.ID != testKeyID {
		t.Errorf("id mismatch: got %q", key.ID)
	}
	if key.Component != "core" {
		t.Errorf("expected component=core, got %q", key.Component)
	}
	if !key.Active {
		t.Errorf("expected active=true")
	}
}

// TestGet_Revoked — revoked key returns 200 with active=false (GetByID ignores active status).
func TestGet_Revoked(t *testing.T) {
	revoked := &store.Key{
		ID:        testKeyID,
		Component: "core",
		Label:     "X",
		Active:    false,
		CreatedAt: time.Now().UTC(),
	}
	h := newTestKeysHandler(&mockStore{
		getByIDFn: func(_ context.Context, _ string) (*store.Key, error) {
			return revoked, nil
		},
	})
	w := inspectKey(h, testKeyID)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var key store.Key
	if err := json.NewDecoder(w.Body).Decode(&key); err != nil {
		t.Fatalf("response not valid JSON: %v", err)
	}
	if key.Active {
		t.Errorf("expected active=false for revoked key")
	}
}

// TestGet_NotFound — AC2: unknown id returns 404 KEY_NOT_FOUND.
func TestGet_NotFound(t *testing.T) {
	h := newTestKeysHandler(&mockStore{
		getByIDFn: func(_ context.Context, _ string) (*store.Key, error) {
			return nil, fmt.Errorf("get key: %w", store.ErrNotFound)
		},
	})
	w := inspectKey(h, testKeyID)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
	var ae apiError
	if err := json.NewDecoder(w.Body).Decode(&ae); err != nil {
		t.Fatalf("response not valid JSON: %v", err)
	}
	if ae.Code != "KEY_NOT_FOUND" {
		t.Errorf("expected KEY_NOT_FOUND, got %q", ae.Code)
	}
	if ae.Message == "" {
		t.Errorf("expected non-empty message")
	}
}

// TestGet_UsageCount — AC3: usage_count round-trips in the response.
func TestGet_UsageCount(t *testing.T) {
	key := makeKey("minion", "Sub")
	key.ID = testKeyID
	key.UsageCount = 42
	h := newTestKeysHandler(&mockStore{
		getByIDFn: func(_ context.Context, _ string) (*store.Key, error) {
			return key, nil
		},
	})
	w := inspectKey(h, testKeyID)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var got store.Key
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("response not valid JSON: %v", err)
	}
	if got.UsageCount != 42 {
		t.Errorf("expected usage_count=42, got %d", got.UsageCount)
	}
}

// TestGet_StoreError — unexpected store error returns 500 with empty body.
func TestGet_StoreError(t *testing.T) {
	h := newTestKeysHandler(&mockStore{
		getByIDFn: func(_ context.Context, _ string) (*store.Key, error) {
			return nil, errors.New("database locked")
		},
	})
	w := inspectKey(h, testKeyID)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("expected empty body on 500, got %q", w.Body.String())
	}
}

// TestCreate_MalformedJSON — non-JSON body returns 400 INVALID_REQUEST.
func TestCreate_MalformedJSON(t *testing.T) {
	h := newTestKeysHandler(&mockStore{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/keys", bytes.NewBufferString("not json"))
	w := httptest.NewRecorder()
	h.Create(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
	var ae apiError
	if err := json.NewDecoder(w.Body).Decode(&ae); err != nil {
		t.Fatalf("response not valid JSON: %v", err)
	}
	if ae.Code != "INVALID_REQUEST" {
		t.Errorf("expected code=INVALID_REQUEST, got %q", ae.Code)
	}
}

// deleteKey is a test helper that calls h.Delete with a properly injected chi route context.
func deleteKey(h *KeysHandler, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/keys/"+id, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	h.Delete(w, req)
	return w
}

// TestDelete_ActiveKey — AC1: revoking an active key returns 204 with no body.
func TestDelete_ActiveKey(t *testing.T) {
	h := newTestKeysHandler(&mockStore{
		revokeKeyFn: func(_ context.Context, _ string) error { return nil },
	})
	w := deleteKey(h, testKeyID)
	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("expected empty body, got %q", w.Body.String())
	}
}

// TestDelete_AlreadyRevoked — AC4: revoking an already-revoked key returns 204 (idempotent).
func TestDelete_AlreadyRevoked(t *testing.T) {
	revoked := &store.Key{ID: testKeyID, Component: "core", Active: false, CreatedAt: time.Now().UTC()}
	h := newTestKeysHandler(&mockStore{
		revokeKeyFn: func(_ context.Context, _ string) error {
			return fmt.Errorf("revoke key: %w", store.ErrNotFound)
		},
		getByIDFn: func(_ context.Context, _ string) (*store.Key, error) {
			return revoked, nil // key exists but active=false
		},
	})
	w := deleteKey(h, testKeyID)
	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
}

// TestDelete_NotFound — AC3: unknown id returns 404 KEY_NOT_FOUND.
func TestDelete_NotFound(t *testing.T) {
	h := newTestKeysHandler(&mockStore{
		revokeKeyFn: func(_ context.Context, _ string) error {
			return fmt.Errorf("revoke key: %w", store.ErrNotFound)
		},
		getByIDFn: func(_ context.Context, _ string) (*store.Key, error) {
			return nil, fmt.Errorf("get key: %w", store.ErrNotFound)
		},
	})
	w := deleteKey(h, testKeyID)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
	var ae apiError
	json.NewDecoder(w.Body).Decode(&ae)
	if ae.Code != "KEY_NOT_FOUND" {
		t.Errorf("expected KEY_NOT_FOUND, got %q", ae.Code)
	}
}

// TestDelete_RevokeStoreError — unexpected RevokeKey error returns 500 with empty body.
func TestDelete_RevokeStoreError(t *testing.T) {
	h := newTestKeysHandler(&mockStore{
		revokeKeyFn: func(_ context.Context, _ string) error {
			return errors.New("database locked")
		},
	})
	w := deleteKey(h, testKeyID)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("expected empty body, got %q", w.Body.String())
	}
}

// TestDelete_GetByIDStoreError — RevokeKey returns ErrNotFound but GetByID errors → 500.
func TestDelete_GetByIDStoreError(t *testing.T) {
	h := newTestKeysHandler(&mockStore{
		revokeKeyFn: func(_ context.Context, _ string) error {
			return fmt.Errorf("revoke key: %w", store.ErrNotFound)
		},
		getByIDFn: func(_ context.Context, _ string) (*store.Key, error) {
			return nil, errors.New("database locked")
		},
	})
	w := deleteKey(h, testKeyID)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("expected empty body, got %q", w.Body.String())
	}
}
