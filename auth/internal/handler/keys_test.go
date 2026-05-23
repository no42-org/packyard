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
	"github.com/no42-org/packyard-auth/internal/audit"
	"github.com/no42-org/packyard-auth/internal/store"
)

// testKeyID is a valid 64-char hex key ID used across inspect tests.
const testKeyID = "aabbccdd" + "aabbccdd" + "aabbccdd" + "aabbccdd" +
	"aabbccdd" + "aabbccdd" + "aabbccdd" + "aabbccdd"

func newTestKeysHandler(s store.KeyStore) *KeysHandler {
	cs := newStubComponentStore()
	cs.comps["core"] = &store.Component{Name: "core", Visibility: "public", RPMSeries: []string{}, RPMOSFamilies: []string{}, RPMArchitectures: []string{}}
	cs.comps["minion"] = &store.Component{Name: "minion", Visibility: "private", RPMSeries: []string{}, RPMOSFamilies: []string{}, RPMArchitectures: []string{}}
	cs.comps["sentinel"] = &store.Component{Name: "sentinel", Visibility: "private", RPMSeries: []string{}, RPMOSFamilies: []string{}, RPMArchitectures: []string{}}
	return &KeysHandler{
		Store:           s,
		ComponentStore:  cs,
		Logger:          slog.Default(),
		ValidComponents: map[string]bool{"core": true, "minion": true, "sentinel": true},
		ValidComponentList: "core, minion, sentinel",
		ComponentVisibility: map[string]string{
			"core":     "public",
			"minion":   "private",
			"sentinel": "private",
		},
	}
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
		createKeyFn: func(_ context.Context, _, component, label string, _ *time.Time) (*store.Key, error) {
			return makeKey(component, label), nil
		},
	})
	w := postKeys(h, `{"account_id":"acct-test","component":"core","label":"Acme Corp - Core","expires_at":null}`)

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
		createKeyFn: func(_ context.Context, _, component, label string, _ *time.Time) (*store.Key, error) {
			return makeKey(component, label), nil
		},
	})
	w := postKeys(h, `{"account_id":"acct-test","component":"minion","label":"Minion Sub"}`)

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
		createKeyFn: func(_ context.Context, _, component, label string, _ *time.Time) (*store.Key, error) {
			return makeKey(component, label), nil
		},
	})
	w := postKeys(h, `{"account_id":"acct-test","component":"sentinel","label":"Sentinel Sub"}`)

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d", w.Code)
	}
}

// TestCreate_InvalidComponent — AC2: unknown component returns 400 INVALID_COMPONENT.
func TestCreate_InvalidComponent(t *testing.T) {
	h := newTestKeysHandler(&mockStore{
		createKeyFn: func(_ context.Context, _, _, _ string, _ *time.Time) (*store.Key, error) {
			t.Fatal("CreateKey must not be called for invalid component")
			return nil, nil
		},
	})
	w := postKeys(h, `{"account_id":"acct-test","component":"invalid","label":"test"}`)

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
		createKeyFn: func(_ context.Context, _, _, _ string, _ *time.Time) (*store.Key, error) {
			t.Fatal("CreateKey must not be called for empty component")
			return nil, nil
		},
	})
	w := postKeys(h, `{"account_id":"acct-test","component":"","label":"test"}`)

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
		createKeyFn: func(_ context.Context, _, component, label string, _ *time.Time) (*store.Key, error) {
			return makeKey(component, label), nil
		},
	})
	body, _ := json.Marshal(map[string]any{"account_id": "acct-test", "component": "core", "label": wantLabel})
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
		createKeyFn: func(_ context.Context, _, _, _ string, _ *time.Time) (*store.Key, error) {
			return nil, errors.New("database locked")
		},
	})
	w := postKeys(h, `{"account_id":"acct-test","component":"core","label":"test"}`)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
	var ae apiError
	json.NewDecoder(w.Body).Decode(&ae)
	if ae.Code != "KEY_CREATE_FAILED" {
		t.Errorf("expected KEY_CREATE_FAILED, got %q", ae.Code)
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
		listKeysFn: func(_ context.Context, component, _ string, _, _ int) ([]*store.Key, error) {
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
		listKeysFn: func(_ context.Context, component, _ string, _, _ int) ([]*store.Key, error) {
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
		listKeysFn: func(_ context.Context, _, _ string, _, _ int) ([]*store.Key, error) {
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
		listKeysFn: func(_ context.Context, _, _ string, _, _ int) ([]*store.Key, error) {
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
		listKeysFn: func(_ context.Context, _, _ string, _, _ int) ([]*store.Key, error) {
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
		listKeysFn: func(_ context.Context, _, _ string, _, _ int) ([]*store.Key, error) {
			return nil, errors.New("database locked")
		},
	})
	w := getKeys(h, "")

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
	var ae apiError
	json.NewDecoder(w.Body).Decode(&ae)
	if ae.Code != "KEY_LIST_FAILED" {
		t.Errorf("expected KEY_LIST_FAILED, got %q", ae.Code)
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
	var ae apiError
	json.NewDecoder(w.Body).Decode(&ae)
	if ae.Code != "KEY_GET_FAILED" {
		t.Errorf("expected KEY_GET_FAILED, got %q", ae.Code)
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

// TestDelete_RevokeStoreError — unexpected RevokeKey error returns 500 KEY_DELETE_FAILED.
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
	var ae apiError
	json.NewDecoder(w.Body).Decode(&ae)
	if ae.Code != "KEY_DELETE_FAILED" {
		t.Errorf("expected KEY_DELETE_FAILED, got %q", ae.Code)
	}
}

// TestDelete_GetByIDStoreError — RevokeKey returns ErrNotFound but GetByID errors → 500 KEY_DELETE_FAILED.
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
	var ae apiError
	json.NewDecoder(w.Body).Decode(&ae)
	if ae.Code != "KEY_DELETE_FAILED" {
		t.Errorf("expected KEY_DELETE_FAILED, got %q", ae.Code)
	}
}

// keyWithVisibility mirrors keyResponse for decoding in visibility tests.
type keyWithVisibility struct {
	store.Key
	ComponentVisibility string `json:"component_visibility"`
}

// TestCreate_ComponentVisibility_Public — Create for a public component includes component_visibility="public".
func TestCreate_ComponentVisibility_Public(t *testing.T) {
	h := newTestKeysHandler(&mockStore{
		createKeyFn: func(_ context.Context, _, component, label string, _ *time.Time) (*store.Key, error) {
			return makeKey(component, label), nil
		},
	})
	w := postKeys(h, `{"account_id":"acct-test","component":"core","label":"test"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", w.Code)
	}
	var got keyWithVisibility
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ComponentVisibility != "public" {
		t.Errorf("expected component_visibility=public, got %q", got.ComponentVisibility)
	}
}

// TestGet_ComponentVisibility_Private — Get for a private component includes component_visibility="private".
func TestGet_ComponentVisibility_Private(t *testing.T) {
	h := newTestKeysHandler(&mockStore{
		getByIDFn: func(_ context.Context, _ string) (*store.Key, error) {
			return makeKey("minion", "sub"), nil
		},
	})
	w := inspectKey(h, testKeyID)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var got keyWithVisibility
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ComponentVisibility != "private" {
		t.Errorf("expected component_visibility=private, got %q", got.ComponentVisibility)
	}
}

// TestList_ComponentVisibility — List returns correct visibility for each key's component.
func TestList_ComponentVisibility(t *testing.T) {
	h := newTestKeysHandler(&mockStore{
		listKeysFn: func(_ context.Context, _, _ string, _, _ int) ([]*store.Key, error) {
			return []*store.Key{makeKey("core", "A"), makeKey("minion", "B")}, nil
		},
	})
	w := getKeys(h, "")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var got []keyWithVisibility
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(got))
	}
	for _, k := range got {
		switch k.Component {
		case "core":
			if k.ComponentVisibility != "public" {
				t.Errorf("core: expected component_visibility=public, got %q", k.ComponentVisibility)
			}
		case "minion":
			if k.ComponentVisibility != "private" {
				t.Errorf("minion: expected component_visibility=private, got %q", k.ComponentVisibility)
			}
		}
	}
}

// TestGet_ComponentVisibility_RemovedComponent — key whose component is absent from the map defaults to "private".
func TestGet_ComponentVisibility_RemovedComponent(t *testing.T) {
	h := &KeysHandler{
		Store:               &mockStore{
			getByIDFn: func(_ context.Context, _ string) (*store.Key, error) {
				return makeKey("removed", "orphan"), nil
			},
		},
		Logger:              slog.Default(),
		ValidComponents:     map[string]bool{"core": true},
		ValidComponentList:  "core",
		ComponentVisibility: map[string]string{"core": "public"},
	}
	w := inspectKey(h, testKeyID)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var got keyWithVisibility
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ComponentVisibility != "private" {
		t.Errorf("removed component: expected component_visibility=private, got %q", got.ComponentVisibility)
	}
}

// ─── Section 3 — account_id wiring on the keys handler ──────────────────────

// stubAccountStore satisfies store.AccountStore for handler tests; only the
// methods exercised by keys.go are implemented (GetAccount). The rest panic
// to surface accidental use.
type stubAccountStore struct {
	getAccountFn func(ctx context.Context, id string) (*store.Account, error)
}

func (s *stubAccountStore) GetAccount(ctx context.Context, id string) (*store.Account, error) {
	if s.getAccountFn != nil {
		return s.getAccountFn(ctx, id)
	}
	return nil, store.ErrAccountNotFound
}
func (s *stubAccountStore) CreateAccount(context.Context, store.AccountInput, string) (*store.Account, error) {
	panic("stubAccountStore.CreateAccount called unexpectedly")
}
func (s *stubAccountStore) ListAccounts(context.Context, store.AccountStatus, int, int) ([]*store.Account, error) {
	panic("stubAccountStore.ListAccounts called unexpectedly")
}
func (s *stubAccountStore) UpdateAccount(context.Context, string, store.AccountUpdate) (*store.Account, error) {
	panic("stubAccountStore.UpdateAccount called unexpectedly")
}
func (s *stubAccountStore) DeleteAccountWithCascade(context.Context, string) (int64, error) {
	panic("stubAccountStore.DeleteAccountWithCascade called unexpectedly")
}
func (s *stubAccountStore) CountActiveAccountKeys(context.Context, string) (int64, error) {
	panic("stubAccountStore.CountActiveAccountKeys called unexpectedly")
}
func (s *stubAccountStore) ListAccountKeys(context.Context, string, int, int) ([]*store.Key, error) {
	panic("stubAccountStore.ListAccountKeys called unexpectedly")
}
func (s *stubAccountStore) CreateKeyForAccount(context.Context, string, string, string, *time.Time) (*store.Key, error) {
	panic("stubAccountStore.CreateKeyForAccount called unexpectedly")
}

// newKeysHandlerWithAccountStore wires a KeysHandler with the supplied
// AccountStore so the new section-3 paths can be exercised.
func newKeysHandlerWithAccountStore(ks store.KeyStore, as store.AccountStore) *KeysHandler {
	h := newTestKeysHandler(ks)
	h.AccountStore = as
	return h
}

// TestCreate_MissingAccountID — § 3.1: body without account_id returns 400.
func TestCreate_MissingAccountID(t *testing.T) {
	h := newTestKeysHandler(&mockStore{
		createKeyFn: func(_ context.Context, _, _, _ string, _ *time.Time) (*store.Key, error) {
			t.Fatal("CreateKey must not be called when account_id is missing")
			return nil, nil
		},
	})
	w := postKeys(h, `{"component":"core","label":"x"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
	var ae apiError
	json.NewDecoder(w.Body).Decode(&ae)
	if ae.Code != "MISSING_ACCOUNT_ID" {
		t.Errorf("want MISSING_ACCOUNT_ID, got %q", ae.Code)
	}
}

// TestCreate_EmptyAccountID — explicit "" same as missing.
func TestCreate_EmptyAccountID(t *testing.T) {
	h := newTestKeysHandler(&mockStore{})
	w := postKeys(h, `{"account_id":"","component":"core","label":"x"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
	var ae apiError
	json.NewDecoder(w.Body).Decode(&ae)
	if ae.Code != "MISSING_ACCOUNT_ID" {
		t.Errorf("want MISSING_ACCOUNT_ID, got %q", ae.Code)
	}
}

// TestCreate_UnknownAccount — § 3.1 + spec keys-api-response-codes:
// unknown account_id returns 404 ACCOUNT_NOT_FOUND (canonical mapping per
// admin-api-error-responses). The store hides deleted accounts via
// GetAccount returning ErrAccountNotFound, so this path covers both.
func TestCreate_UnknownAccount(t *testing.T) {
	ks := &mockStore{
		createKeyFn: func(_ context.Context, _, _, _ string, _ *time.Time) (*store.Key, error) {
			t.Fatal("CreateKey must not be called when account does not exist")
			return nil, nil
		},
	}
	as := &stubAccountStore{
		getAccountFn: func(_ context.Context, _ string) (*store.Account, error) {
			return nil, store.ErrAccountNotFound
		},
	}
	h := newKeysHandlerWithAccountStore(ks, as)
	w := postKeys(h, `{"account_id":"ghost","component":"core","label":"x"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
	var ae apiError
	json.NewDecoder(w.Body).Decode(&ae)
	if ae.Code != "ACCOUNT_NOT_FOUND" {
		t.Errorf("want ACCOUNT_NOT_FOUND, got %q", ae.Code)
	}
}

// TestCreate_KnownAccount — § 3.1: known account flows through to CreateKey
// and the response payload includes account_id (§ 3.2).
func TestCreate_KnownAccount(t *testing.T) {
	const wantAccountID = "acct-known"
	ks := &mockStore{
		createKeyFn: func(_ context.Context, accountID, component, label string, _ *time.Time) (*store.Key, error) {
			if accountID != wantAccountID {
				t.Errorf("CreateKey accountID: want %q, got %q", wantAccountID, accountID)
			}
			k := makeKey(component, label)
			k.AccountID = accountID
			return k, nil
		},
	}
	as := &stubAccountStore{
		getAccountFn: func(_ context.Context, id string) (*store.Account, error) {
			if id != wantAccountID {
				t.Errorf("GetAccount id: want %q, got %q", wantAccountID, id)
			}
			return &store.Account{ID: id, Status: store.AccountStatusActive}, nil
		},
	}
	h := newKeysHandlerWithAccountStore(ks, as)

	body := fmt.Sprintf(`{"account_id":%q,"component":"core","label":"x"}`, wantAccountID)
	w := postKeys(h, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}

	// § 3.2: response must include account_id.
	var raw map[string]any
	if err := json.NewDecoder(w.Body).Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got, _ := raw["account_id"].(string); got != wantAccountID {
		t.Errorf("response.account_id: want %q, got %v", wantAccountID, raw["account_id"])
	}
}

// TestList_FilterByAccount — § 3.3: ?account= reaches the store filter.
func TestList_FilterByAccount(t *testing.T) {
	const wantAccount = "acct-filter"
	h := newTestKeysHandler(&mockStore{
		listKeysFn: func(_ context.Context, componentFilter, accountFilter string, _, _ int) ([]*store.Key, error) {
			if componentFilter != "" {
				t.Errorf("componentFilter: want empty, got %q", componentFilter)
			}
			if accountFilter != wantAccount {
				t.Errorf("accountFilter: want %q, got %q", wantAccount, accountFilter)
			}
			k := makeKey("core", "scoped")
			k.AccountID = wantAccount
			return []*store.Key{k}, nil
		},
	})
	w := getKeys(h, "?account="+wantAccount)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var got []map[string]any
	json.NewDecoder(w.Body).Decode(&got)
	if len(got) != 1 || got[0]["account_id"] != wantAccount {
		t.Errorf("want one key with account_id=%q, got %v", wantAccount, got)
	}
}

// TestList_FilterByComponentAndAccount — § 3.3: both filters combine.
func TestList_FilterByComponentAndAccount(t *testing.T) {
	h := newTestKeysHandler(&mockStore{
		listKeysFn: func(_ context.Context, componentFilter, accountFilter string, _, _ int) ([]*store.Key, error) {
			if componentFilter != "core" || accountFilter != "acct-x" {
				t.Errorf("filters: want (core,acct-x), got (%q,%q)", componentFilter, accountFilter)
			}
			return []*store.Key{}, nil
		},
	})
	w := getKeys(h, "?component=core&account=acct-x")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

// ─── Audit wiring (§ 6 review CRITICAL fix) ─────────────────────────────────

// keysAuditRecorder is a minimal audit.Auditor for tests that captures
// emitted entries so assertions can verify key.issue / key.revoke firing.
type keysAuditRecorder struct{ entries []audit.Entry }

func (k *keysAuditRecorder) Write(_ context.Context, e audit.Entry) {
	k.entries = append(k.entries, e)
}

// newKeysHandlerWithAudit returns a KeysHandler whose Auditor is the supplied
// recorder so tests can assert audit emission. KeyStore is the supplied mock.
func newKeysHandlerWithAudit(ks store.KeyStore, rec audit.Auditor) *KeysHandler {
	h := newTestKeysHandler(ks)
	h.Auditor = rec
	return h
}

// TestCreate_EmitsKeyIssueAudit verifies POST /api/v1/keys persists a
// `key.issue` audit row per spec § "Audit log coverage".
func TestCreate_EmitsKeyIssueAudit(t *testing.T) {
	rec := &keysAuditRecorder{}
	ks := &mockStore{
		createKeyFn: func(_ context.Context, accountID, component, label string, _ *time.Time) (*store.Key, error) {
			k := makeKey(component, label)
			k.AccountID = accountID
			return k, nil
		},
	}
	as := &stubAccountStore{
		getAccountFn: func(_ context.Context, id string) (*store.Account, error) {
			return &store.Account{ID: id, Status: store.AccountStatusActive}, nil
		},
	}
	h := newKeysHandlerWithAudit(ks, rec)
	h.AccountStore = as

	w := postKeys(h, `{"account_id":"acct-1","component":"core","label":"x"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != "key.issue" {
		t.Errorf("want one key.issue audit row, got %+v", rec.entries)
	}
	if rec.entries[0].TargetType != "key" {
		t.Errorf("target_type: want 'key', got %q", rec.entries[0].TargetType)
	}
}

// TestDelete_EmitsKeyRevokeAudit verifies DELETE /api/v1/keys/{id} persists
// a `key.revoke` audit row on successful revocation.
func TestDelete_EmitsKeyRevokeAudit(t *testing.T) {
	rec := &keysAuditRecorder{}
	ks := &mockStore{
		revokeKeyFn: func(_ context.Context, _ string) error { return nil },
	}
	h := newKeysHandlerWithAudit(ks, rec)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/keys/"+testKeyID, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", testKeyID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	h.Delete(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", w.Code)
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != "key.revoke" {
		t.Errorf("want one key.revoke audit row, got %+v", rec.entries)
	}
}

// TestDelete_AlreadyRevokedNoAudit — idempotent re-delete must NOT emit a
// duplicate audit row (the call had no effect).
func TestDelete_AlreadyRevokedNoAudit(t *testing.T) {
	rec := &keysAuditRecorder{}
	ks := &mockStore{
		revokeKeyFn: func(_ context.Context, _ string) error { return store.ErrNotFound },
		getByIDFn: func(_ context.Context, _ string) (*store.Key, error) {
			return makeKey("core", "revoked"), nil // exists, so already-revoked path
		},
	}
	h := newKeysHandlerWithAudit(ks, rec)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/keys/"+testKeyID, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", testKeyID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	h.Delete(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d", w.Code)
	}
	if len(rec.entries) != 0 {
		t.Errorf("already-revoked re-delete should NOT audit; got %+v", rec.entries)
	}
}
