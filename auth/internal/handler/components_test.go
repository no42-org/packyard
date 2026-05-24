package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/no42-org/packyard-auth/internal/store"
)

// ─── in-memory ComponentStore stub ───────────────────────────────────────────

type stubComponentStore struct {
	comps    map[string]*store.Component
	keys     map[string]int64 // component → active key count (for impact preview)
	updateFn func(ctx context.Context, name, visibility string) (*store.Component, error)
}

func newStubComponentStore() *stubComponentStore {
	return &stubComponentStore{
		comps: map[string]*store.Component{},
		keys:  map[string]int64{},
	}
}

func (s *stubComponentStore) CreateComponent(_ context.Context, comp *store.Component) (*store.Component, error) {
	if _, ok := s.comps[comp.Name]; ok {
		return nil, store.ErrComponentExists
	}
	vis := comp.Visibility
	if vis == "" {
		vis = "private"
	}
	c := &store.Component{
		Name:             comp.Name,
		Visibility:       vis,
		RPMSeries:        comp.RPMSeries,
		RPMOSFamilies:    comp.RPMOSFamilies,
		RPMArchitectures: comp.RPMArchitectures,
		CreatedAt:        time.Now().UTC(),
	}
	s.comps[comp.Name] = c
	return c, nil
}

func (s *stubComponentStore) GetComponent(_ context.Context, name string) (*store.Component, error) {
	c, ok := s.comps[name]
	if !ok {
		return nil, store.ErrComponentNotFound
	}
	return c, nil
}

func (s *stubComponentStore) ListComponents(_ context.Context, offset, limit int) ([]*store.Component, error) {
	out := make([]*store.Component, 0, len(s.comps))
	for _, c := range s.comps {
		out = append(out, c)
	}
	if offset >= len(out) {
		return []*store.Component{}, nil
	}
	out = out[offset:]
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *stubComponentStore) DeleteComponent(_ context.Context, name string) error {
	if _, ok := s.comps[name]; !ok {
		return store.ErrComponentNotFound
	}
	delete(s.comps, name)
	return nil
}

func (s *stubComponentStore) RevokeComponentKeys(_ context.Context, component string) (int64, error) {
	n := s.keys[component]
	s.keys[component] = 0
	return n, nil
}

func (s *stubComponentStore) CountActiveComponentKeys(_ context.Context, component string) (int64, error) {
	return s.keys[component], nil
}

func (s *stubComponentStore) DeleteComponentWithRevoke(ctx context.Context, name string) (int64, error) {
	n, err := s.RevokeComponentKeys(ctx, name)
	if err != nil {
		return 0, err
	}
	if err := s.DeleteComponent(ctx, name); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *stubComponentStore) UpdateComponentVisibility(ctx context.Context, name, visibility string) (*store.Component, error) {
	if s.updateFn != nil {
		return s.updateFn(ctx, name, visibility)
	}
	c, ok := s.comps[name]
	if !ok {
		return nil, store.ErrComponentNotFound
	}
	c.Visibility = visibility
	s.comps[name] = c // explicit re-store so callers see the mutation via map lookup
	return c, nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func newTestComponentsHandler(t *testing.T) (*ComponentsHandler, *stubComponentStore) {
	t.Helper()
	st := newStubComponentStore()
	h := NewComponentsHandler(st, slog.Default(), t.TempDir())
	return h, st
}

func chiRequest(method, path, urlPattern string, body []byte) *http.Request {
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, path, bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	// Wire chi URL params.
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("name", filepath.Base(urlPattern))
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	return r
}

// ─── POST /api/v1/components ─────────────────────────────────────────────────

func TestComponentCreate_Success(t *testing.T) {
	h, _ := newTestComponentsHandler(t)

	body, _ := json.Marshal(createComponentRequest{
		Name:             "core",
		Visibility:       "private",
		RPMSeries:        []string{"2025"},
		RPMOSFamilies:    []string{"el9"},
		RPMArchitectures: []string{"x86_64"},
	})

	r := httptest.NewRequest(http.MethodPost, "/api/v1/components", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.Create(w, r)

	if w.Code != http.StatusCreated {
		t.Errorf("status: want 201, got %d — body: %s", w.Code, w.Body.String())
	}

	var comp store.Component
	if err := json.NewDecoder(w.Body).Decode(&comp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if comp.Name != "core" {
		t.Errorf("name: want core, got %q", comp.Name)
	}

	// Verify RPM directories were created
	dir := filepath.Join(h.RPMDataRoot, "rpm", "core", "2025", "el9-x86_64")
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		t.Errorf("RPM dir not created: %s", dir)
	}
}

func TestComponentCreate_Duplicate(t *testing.T) {
	h, st := newTestComponentsHandler(t)
	_ = st // pre-populate
	ctx := context.Background()
	_, _ = h.Store.CreateComponent(ctx, &store.Component{
		Name: "core", Visibility: "private",
		RPMSeries: []string{}, RPMOSFamilies: []string{}, RPMArchitectures: []string{},
	})

	body, _ := json.Marshal(createComponentRequest{Name: "core"})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/components", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Create(w, r)

	if w.Code != http.StatusConflict {
		t.Errorf("status: want 409, got %d", w.Code)
	}
	assertErrorCode(t, w, "COMPONENT_EXISTS")
}

func TestComponentCreate_InvalidVisibility(t *testing.T) {
	h, _ := newTestComponentsHandler(t)

	body, _ := json.Marshal(createComponentRequest{Name: "core", Visibility: "restricted"})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/components", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Create(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d", w.Code)
	}
	assertErrorCode(t, w, "INVALID_VISIBILITY")
}

func TestComponentCreate_DefaultVisibility(t *testing.T) {
	h, _ := newTestComponentsHandler(t)

	body, _ := json.Marshal(createComponentRequest{Name: "core"}) // visibility omitted

	r := httptest.NewRequest(http.MethodPost, "/api/v1/components", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Create(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("status: want 201, got %d — body: %s", w.Code, w.Body.String())
	}
	var comp store.Component
	if err := json.NewDecoder(w.Body).Decode(&comp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if comp.Visibility != "private" {
		t.Errorf("visibility: want private (default), got %q", comp.Visibility)
	}
}

func TestComponentCreate_EmptyName(t *testing.T) {
	h, _ := newTestComponentsHandler(t)

	body, _ := json.Marshal(createComponentRequest{Name: ""})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/components", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.Create(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d", w.Code)
	}
}

// ─── GET /api/v1/components ───────────────────────────────────────────────────

func TestComponentList_Empty(t *testing.T) {
	h, _ := newTestComponentsHandler(t)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/components", nil)
	w := httptest.NewRecorder()
	h.List(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", w.Code)
	}
	var comps []*store.Component
	if err := json.NewDecoder(w.Body).Decode(&comps); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(comps) != 0 {
		t.Errorf("expected empty array, got %d items", len(comps))
	}
}

func TestComponentList_ReturnsAll(t *testing.T) {
	h, st := newTestComponentsHandler(t)
	ctx := context.Background()
	for _, name := range []string{"core", "minion"} {
		_, _ = st.CreateComponent(ctx, &store.Component{
			Name: name, Visibility: "private",
			RPMSeries: []string{}, RPMOSFamilies: []string{}, RPMArchitectures: []string{},
		})
	}

	r := httptest.NewRequest(http.MethodGet, "/api/v1/components", nil)
	w := httptest.NewRecorder()
	h.List(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", w.Code)
	}
	var comps []*store.Component
	if err := json.NewDecoder(w.Body).Decode(&comps); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(comps) != 2 {
		t.Errorf("expected 2 components, got %d", len(comps))
	}
}

// ─── GET /api/v1/components/{name} ───────────────────────────────────────────

func TestComponentGetOne_Found(t *testing.T) {
	h, st := newTestComponentsHandler(t)
	ctx := context.Background()
	_, _ = st.CreateComponent(ctx, &store.Component{
		Name: "core", Visibility: "private",
		RPMSeries: []string{"2025"}, RPMOSFamilies: []string{"el9"}, RPMArchitectures: []string{"x86_64"},
	})

	r := chiRequest(http.MethodGet, "/api/v1/components/core", "core", nil)
	w := httptest.NewRecorder()
	h.GetOne(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", w.Code)
	}
	var comp store.Component
	if err := json.NewDecoder(w.Body).Decode(&comp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if comp.Name != "core" {
		t.Errorf("name: want core, got %q", comp.Name)
	}
}

func TestComponentGetOne_NotFound(t *testing.T) {
	h, _ := newTestComponentsHandler(t)
	r := chiRequest(http.MethodGet, "/api/v1/components/unknown", "unknown", nil)
	w := httptest.NewRecorder()
	h.GetOne(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status: want 404, got %d", w.Code)
	}
	assertErrorCode(t, w, "COMPONENT_NOT_FOUND")
}

// ─── DELETE /api/v1/components/{name} ────────────────────────────────────────

func TestComponentDelete_WithoutConfirm(t *testing.T) {
	h, st := newTestComponentsHandler(t)
	ctx := context.Background()
	_, _ = st.CreateComponent(ctx, &store.Component{
		Name: "core", Visibility: "private",
		RPMSeries: []string{"2025"}, RPMOSFamilies: []string{}, RPMArchitectures: []string{},
	})
	st.keys["core"] = 4

	r := chiRequest(http.MethodDelete, "/api/v1/components/core", "core", nil)
	w := httptest.NewRecorder()
	h.Delete(w, r)

	if w.Code != http.StatusConflict {
		t.Errorf("status: want 409, got %d", w.Code)
	}
	var body deleteImpact
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode impact response: %v", err)
	}
	if body.Code != "CONFIRM_REQUIRED" {
		t.Errorf("code: want CONFIRM_REQUIRED, got %q", body.Code)
	}
	if body.Impact.KeysRevoked != 4 {
		t.Errorf("impact.keys_revoked: want 4, got %d", body.Impact.KeysRevoked)
	}
	if len(body.Impact.RPMSeriesRemoved) != 1 || body.Impact.RPMSeriesRemoved[0] != "2025" {
		t.Errorf("impact.rpm_series_removed: want [2025], got %v", body.Impact.RPMSeriesRemoved)
	}
}

func TestComponentDelete_WrongConfirm(t *testing.T) {
	h, st := newTestComponentsHandler(t)
	ctx := context.Background()
	_, _ = st.CreateComponent(ctx, &store.Component{
		Name: "core", Visibility: "private",
		RPMSeries: []string{}, RPMOSFamilies: []string{}, RPMArchitectures: []string{},
	})

	r := chiRequest(http.MethodDelete, "/api/v1/components/core?confirm=CORE", "core", nil)
	r.URL.RawQuery = "confirm=CORE"
	w := httptest.NewRecorder()
	h.Delete(w, r)

	if w.Code != http.StatusConflict {
		t.Errorf("status: want 409, got %d — wrong confirm case should be rejected", w.Code)
	}
}

func TestComponentDelete_Success(t *testing.T) {
	h, st := newTestComponentsHandler(t)
	ctx := context.Background()
	_, _ = st.CreateComponent(ctx, &store.Component{
		Name: "core", Visibility: "private",
		RPMSeries: []string{}, RPMOSFamilies: []string{}, RPMArchitectures: []string{},
	})
	st.keys["core"] = 3

	r := chiRequest(http.MethodDelete, "/api/v1/components/core?confirm=core", "core", nil)
	r.URL.RawQuery = "confirm=core"
	w := httptest.NewRecorder()
	h.Delete(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d — body: %s", w.Code, w.Body.String())
	}
	var result deleteResult
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.KeysRevoked != 3 {
		t.Errorf("keys_revoked: want 3, got %d", result.KeysRevoked)
	}

	// Component should be gone
	_, err := st.GetComponent(ctx, "core")
	if err == nil {
		t.Error("component should be deleted")
	}
}

func TestComponentDelete_NotFound(t *testing.T) {
	h, _ := newTestComponentsHandler(t)
	r := chiRequest(http.MethodDelete, "/api/v1/components/unknown?confirm=unknown", "unknown", nil)
	r.URL.RawQuery = "confirm=unknown"
	w := httptest.NewRecorder()
	h.Delete(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status: want 404, got %d", w.Code)
	}
}

// ─── PATCH /api/v1/components/{name} ─────────────────────────────────────────

func TestComponentUpdate_PrivateToPublic(t *testing.T) {
	h, st := newTestComponentsHandler(t)
	ctx := context.Background()
	_, _ = st.CreateComponent(ctx, &store.Component{
		Name: "core", Visibility: "private",
		RPMSeries: []string{}, RPMOSFamilies: []string{}, RPMArchitectures: []string{},
	})

	body, _ := json.Marshal(updateComponentRequest{Visibility: "public"})
	r := chiRequest(http.MethodPatch, "/api/v1/components/core", "core", body)
	w := httptest.NewRecorder()
	h.Update(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d — body: %s", w.Code, w.Body.String())
	}
	var comp store.Component
	if err := json.NewDecoder(w.Body).Decode(&comp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if comp.Visibility != "public" {
		t.Errorf("visibility: want public, got %q", comp.Visibility)
	}
}

func TestComponentUpdate_PublicToPrivate(t *testing.T) {
	h, st := newTestComponentsHandler(t)
	ctx := context.Background()
	_, _ = st.CreateComponent(ctx, &store.Component{
		Name: "core", Visibility: "public",
		RPMSeries: []string{}, RPMOSFamilies: []string{}, RPMArchitectures: []string{},
	})

	body, _ := json.Marshal(updateComponentRequest{Visibility: "private"})
	r := chiRequest(http.MethodPatch, "/api/v1/components/core", "core", body)
	w := httptest.NewRecorder()
	h.Update(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", w.Code)
	}
	var comp store.Component
	if err := json.NewDecoder(w.Body).Decode(&comp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if comp.Visibility != "private" {
		t.Errorf("visibility: want private, got %q", comp.Visibility)
	}
}

func TestComponentUpdate_NotFound(t *testing.T) {
	h, _ := newTestComponentsHandler(t)
	body, _ := json.Marshal(updateComponentRequest{Visibility: "public"})
	r := chiRequest(http.MethodPatch, "/api/v1/components/unknown", "unknown", body)
	w := httptest.NewRecorder()
	h.Update(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("status: want 404, got %d", w.Code)
	}
	assertErrorCode(t, w, "COMPONENT_NOT_FOUND")
}

func TestComponentUpdate_InvalidVisibility(t *testing.T) {
	h, _ := newTestComponentsHandler(t)
	body, _ := json.Marshal(updateComponentRequest{Visibility: "restricted"})
	r := chiRequest(http.MethodPatch, "/api/v1/components/core", "core", body)
	w := httptest.NewRecorder()
	h.Update(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d", w.Code)
	}
	assertErrorCode(t, w, "INVALID_VISIBILITY")
}

func TestComponentUpdate_MalformedBody(t *testing.T) {
	h, _ := newTestComponentsHandler(t)
	r := chiRequest(http.MethodPatch, "/api/v1/components/core", "core", []byte("not-json"))
	w := httptest.NewRecorder()
	h.Update(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d", w.Code)
	}
	assertErrorCode(t, w, "INVALID_REQUEST")
}

func TestComponentUpdate_StoreError(t *testing.T) {
	h, cs := newTestComponentsHandler(t)
	cs.updateFn = func(_ context.Context, _, _ string) (*store.Component, error) {
		return nil, errors.New("database locked")
	}

	body, _ := json.Marshal(updateComponentRequest{Visibility: "public"})
	r := chiRequest(http.MethodPatch, "/api/v1/components/core", "core", body)
	w := httptest.NewRecorder()
	h.Update(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status: want 500, got %d", w.Code)
	}
	assertErrorCode(t, w, "COMPONENT_UPDATE_FAILED")
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func assertErrorCode(t *testing.T, w *httptest.ResponseRecorder, code string) {
	t.Helper()
	var e apiError
	if err := json.NewDecoder(w.Body).Decode(&e); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if e.Code != code {
		t.Errorf("error code: want %q, got %q", code, e.Code)
	}
}
