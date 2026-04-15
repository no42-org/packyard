package handler

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/no42-org/packyard-auth/internal/store"
)

// mockStore is a test double for store.KeyStore used in handler tests.
// The architecture explicitly permits mocking at the handler layer (AC7).
type mockStore struct {
	getByValueFn     func(ctx context.Context, value string) (*store.Key, error)
	incrementUsageFn func(ctx context.Context, id string) error
	createKeyFn      func(ctx context.Context, component, label string, expiresAt *time.Time) (*store.Key, error)
	listKeysFn       func(ctx context.Context, component string) ([]*store.Key, error)
	getByIDFn        func(ctx context.Context, id string) (*store.Key, error)
	revokeKeyFn      func(ctx context.Context, id string) error
}

func (m *mockStore) GetByValue(ctx context.Context, value string) (*store.Key, error) {
	return m.getByValueFn(ctx, value)
}

func (m *mockStore) IncrementUsage(ctx context.Context, id string) error {
	if m.incrementUsageFn != nil {
		return m.incrementUsageFn(ctx, id)
	}
	return nil
}

func (m *mockStore) CreateKey(ctx context.Context, component, label string, expiresAt *time.Time) (*store.Key, error) {
	if m.createKeyFn != nil {
		return m.createKeyFn(ctx, component, label, expiresAt)
	}
	return nil, errors.New("not implemented")
}

func (m *mockStore) ListKeys(ctx context.Context, component string) ([]*store.Key, error) {
	if m.listKeysFn != nil {
		return m.listKeysFn(ctx, component)
	}
	return nil, errors.New("not implemented")
}

func (m *mockStore) GetByID(ctx context.Context, id string) (*store.Key, error) {
	if m.getByIDFn != nil {
		return m.getByIDFn(ctx, id)
	}
	return nil, errors.New("not implemented")
}

func (m *mockStore) RevokeKey(ctx context.Context, id string) error {
	if m.revokeKeyFn != nil {
		return m.revokeKeyFn(ctx, id)
	}
	return errors.New("not implemented")
}

// validKey is a 64-char hex string that passes the length check.
const validKey = "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"

func basicAuthHeader(key string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte("subscriber:"+key))
}

// newTestComponentStore returns a stub with "core" (private) and "minion" (private) pre-seeded.
func newTestComponentStore() *stubComponentStore {
	cs := newStubComponentStore()
	cs.comps["core"] = &store.Component{Name: "core", Visibility: "private"}
	cs.comps["minion"] = &store.Component{Name: "minion", Visibility: "private"}
	return cs
}

func newTestHandler(s store.KeyStore) *ForwardAuthHandler {
	return &ForwardAuthHandler{
		Store:          s,
		ComponentStore: newTestComponentStore(),
		Logger:         slog.Default(),
	}
}

func TestNewForwardAuthHandler_Constructs(t *testing.T) {
	h := NewForwardAuthHandler(&mockStore{}, newTestComponentStore(), slog.Default())
	if h.Store == nil {
		t.Error("Store should not be nil")
	}
	if h.ComponentStore == nil {
		t.Error("ComponentStore should not be nil")
	}
}

func TestForwardAuth_ValidKey(t *testing.T) {
	h := newTestHandler(&mockStore{
		getByValueFn: func(_ context.Context, value string) (*store.Key, error) {
			return &store.Key{ID: value, Component: "core", Active: true}, nil
		},
	})
	req := httptest.NewRequest("GET", "/auth", nil)
	req.Header.Set("Authorization", basicAuthHeader(validKey))
	req.Header.Set("X-Forwarded-Uri", "/rpm/core/2025/el9-x86_64/")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("expected empty body, got %q", w.Body.String())
	}
}

func TestForwardAuth_ScopeMismatch(t *testing.T) {
	h := newTestHandler(&mockStore{
		getByValueFn: func(_ context.Context, value string) (*store.Key, error) {
			return &store.Key{ID: value, Component: "minion", Active: true}, nil
		},
	})
	req := httptest.NewRequest("GET", "/auth", nil)
	req.Header.Set("Authorization", basicAuthHeader(validKey))
	req.Header.Set("X-Forwarded-Uri", "/rpm/core/2025/el9-x86_64/lts-core-2025.rpm")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("expected empty body, got %q", w.Body.String())
	}
}

func TestForwardAuth_RevokedKey(t *testing.T) {
	h := newTestHandler(&mockStore{
		getByValueFn: func(_ context.Context, _ string) (*store.Key, error) {
			return nil, fmt.Errorf("get key: %w", store.ErrRevoked)
		},
	})
	req := httptest.NewRequest("GET", "/auth", nil)
	req.Header.Set("Authorization", basicAuthHeader(validKey))
	req.Header.Set("X-Forwarded-Uri", "/rpm/core/2025/el9-x86_64/")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestForwardAuth_NotFound(t *testing.T) {
	h := newTestHandler(&mockStore{
		getByValueFn: func(_ context.Context, _ string) (*store.Key, error) {
			return nil, fmt.Errorf("get key: %w", store.ErrNotFound)
		},
	})
	req := httptest.NewRequest("GET", "/auth", nil)
	req.Header.Set("Authorization", basicAuthHeader(validKey))
	req.Header.Set("X-Forwarded-Uri", "/rpm/core/2025/el9-x86_64/")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestForwardAuth_MissingAuthHeader(t *testing.T) {
	h := newTestHandler(&mockStore{
		getByValueFn: func(_ context.Context, _ string) (*store.Key, error) {
			t.Fatal("GetByValue should not be called when no Authorization header")
			return nil, nil
		},
	})
	req := httptest.NewRequest("GET", "/auth", nil)
	req.Header.Set("X-Forwarded-Uri", "/rpm/core/2025/el9-x86_64/")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestForwardAuth_NonHexKey(t *testing.T) {
	// 64 chars but non-hex — must be rejected before the store is consulted.
	nonHexKey := strings.Repeat("!", 64)
	h := newTestHandler(&mockStore{
		getByValueFn: func(_ context.Context, _ string) (*store.Key, error) {
			t.Fatal("GetByValue should not be called for non-hex key")
			return nil, nil
		},
	})
	req := httptest.NewRequest("GET", "/auth", nil)
	req.Header.Set("Authorization", basicAuthHeader(nonHexKey))
	req.Header.Set("X-Forwarded-Uri", "/rpm/core/2025/el9-x86_64/")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestForwardAuth_WrongKeyLength(t *testing.T) {
	shortKey := strings.Repeat("a", 32) // 32 chars, not 64
	h := newTestHandler(&mockStore{
		getByValueFn: func(_ context.Context, _ string) (*store.Key, error) {
			t.Fatal("GetByValue should not be called for wrong-length key")
			return nil, nil
		},
	})
	req := httptest.NewRequest("GET", "/auth", nil)
	req.Header.Set("Authorization", basicAuthHeader(shortKey))
	req.Header.Set("X-Forwarded-Uri", "/rpm/core/2025/el9-x86_64/")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestForwardAuth_MalformedAuthHeader(t *testing.T) {
	// AC4: malformed Authorization header (non-Basic scheme) → 401
	h := newTestHandler(&mockStore{
		getByValueFn: func(_ context.Context, _ string) (*store.Key, error) {
			t.Fatal("GetByValue should not be called for malformed auth header")
			return nil, nil
		},
	})
	req := httptest.NewRequest("GET", "/auth", nil)
	req.Header.Set("Authorization", "Bearer sometoken")
	req.Header.Set("X-Forwarded-Uri", "/rpm/core/2025/el9-x86_64/")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestForwardAuth_UnrecognisedForwardedUri(t *testing.T) {
	// Handler's !ok branch from extractComponent — e.g. /gpg/ path has no auth middleware
	// but if it somehow reaches /auth, or an empty header is sent, the handler must return 401.
	h := newTestHandler(&mockStore{
		getByValueFn: func(_ context.Context, value string) (*store.Key, error) {
			return &store.Key{ID: value, Component: "core", Active: true}, nil
		},
	})
	cases := []string{"", "/", "/gpg/lts.asc", "/unknown/path"}
	for _, uri := range cases {
		req := httptest.NewRequest("GET", "/auth", nil)
		req.Header.Set("Authorization", basicAuthHeader(validKey))
		req.Header.Set("X-Forwarded-Uri", uri)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("uri=%q: expected 401, got %d", uri, w.Code)
		}
	}
}

func TestForwardAuth_StoreError(t *testing.T) {
	h := newTestHandler(&mockStore{
		getByValueFn: func(_ context.Context, _ string) (*store.Key, error) {
			return nil, errors.New("database connection lost")
		},
	})
	req := httptest.NewRequest("GET", "/auth", nil)
	req.Header.Set("Authorization", basicAuthHeader(validKey))
	req.Header.Set("X-Forwarded-Uri", "/rpm/core/2025/el9-x86_64/")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("expected empty body, got %q", w.Body.String())
	}
}

func newPublicCoreHandler(keyStore store.KeyStore) *ForwardAuthHandler {
	cs := newStubComponentStore()
	cs.comps["core"] = &store.Component{Name: "core", Visibility: "public"}
	cs.comps["minion"] = &store.Component{Name: "minion", Visibility: "private"}
	return &ForwardAuthHandler{Store: keyStore, ComponentStore: cs, Logger: slog.Default()}
}

func TestForwardAuth_PublicComponent_NoCreds(t *testing.T) {
	h := newPublicCoreHandler(&mockStore{})
	req := httptest.NewRequest("GET", "/auth", nil)
	req.Header.Set("X-Forwarded-Uri", "/rpm/core/2025/el9-x86_64/")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for public component with no creds, got %d", w.Code)
	}
}

func TestForwardAuth_PublicComponent_WithCreds(t *testing.T) {
	// Credentials are present but bypassed entirely — key store is never consulted.
	h := newPublicCoreHandler(&mockStore{
		getByValueFn: func(_ context.Context, _ string) (*store.Key, error) {
			t.Fatal("GetByValue should not be called for public component")
			return nil, nil
		},
	})
	req := httptest.NewRequest("GET", "/auth", nil)
	req.Header.Set("Authorization", basicAuthHeader(validKey))
	req.Header.Set("X-Forwarded-Uri", "/rpm/core/2025/el9-x86_64/")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for public component with any creds, got %d", w.Code)
	}
}

func TestForwardAuth_PublicComponent_MalformedAuth(t *testing.T) {
	// Malformed Authorization header is also bypassed for public components.
	h := newPublicCoreHandler(&mockStore{
		getByValueFn: func(_ context.Context, _ string) (*store.Key, error) {
			t.Fatal("GetByValue should not be called for public component")
			return nil, nil
		},
	})
	req := httptest.NewRequest("GET", "/auth", nil)
	req.Header.Set("Authorization", "Bearer not-basic-auth")
	req.Header.Set("X-Forwarded-Uri", "/rpm/core/2025/el9-x86_64/")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for public component with malformed auth, got %d", w.Code)
	}
}

func TestForwardAuth_PrivateComponent_NoCreds(t *testing.T) {
	h := newPublicCoreHandler(&mockStore{})
	req := httptest.NewRequest("GET", "/auth", nil)
	req.Header.Set("X-Forwarded-Uri", "/rpm/minion/2025/el9-x86_64/")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for private component with no creds, got %d", w.Code)
	}
}

func TestForwardAuth_NonexistentComponent(t *testing.T) {
	// Unknown components return 401 regardless of auth state — prevents component enumeration.
	// (Previously returned 404 for authenticated callers; live DB lookup returns 401 uniformly.)
	h := newTestHandler(&mockStore{
		getByValueFn: func(_ context.Context, _ string) (*store.Key, error) {
			t.Fatal("GetByValue should not be called when component is unknown")
			return nil, nil
		},
	})
	for _, withAuth := range []bool{true, false} {
		req := httptest.NewRequest("GET", "/auth", nil)
		if withAuth {
			req.Header.Set("Authorization", basicAuthHeader(validKey))
		}
		req.Header.Set("X-Forwarded-Uri", "/rpm/unknown/2025/el9-x86_64/")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("auth=%v: expected 401 for unknown component, got %d", withAuth, w.Code)
		}
	}
}

func TestForwardAuth_ComponentStoreError(t *testing.T) {
	// A component store error fails closed (503), not open.
	cs := newStubComponentStore()
	cs.comps["core"] = &store.Component{Name: "core", Visibility: "private"}
	// Override GetComponent to simulate a DB error by using a wrapper.
	errCS := &errComponentStore{inner: cs, errOn: "core"}
	h := &ForwardAuthHandler{
		Store:          &mockStore{},
		ComponentStore: errCS,
		Logger:         slog.Default(),
	}
	req := httptest.NewRequest("GET", "/auth", nil)
	req.Header.Set("Authorization", basicAuthHeader(validKey))
	req.Header.Set("X-Forwarded-Uri", "/rpm/core/2025/el9-x86_64/")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 on component store error, got %d", w.Code)
	}
}

// errComponentStore wraps a stubComponentStore and injects an error for a specific component name.
type errComponentStore struct {
	inner *stubComponentStore
	errOn string
}

func (e *errComponentStore) GetComponent(ctx context.Context, name string) (*store.Component, error) {
	if name == e.errOn {
		return nil, errors.New("database connection lost")
	}
	return e.inner.GetComponent(ctx, name)
}
func (e *errComponentStore) CreateComponent(ctx context.Context, c *store.Component) (*store.Component, error) {
	return e.inner.CreateComponent(ctx, c)
}
func (e *errComponentStore) ListComponents(ctx context.Context) ([]*store.Component, error) {
	return e.inner.ListComponents(ctx)
}
func (e *errComponentStore) DeleteComponent(ctx context.Context, name string) error {
	return e.inner.DeleteComponent(ctx, name)
}
func (e *errComponentStore) RevokeComponentKeys(ctx context.Context, component string) (int64, error) {
	return e.inner.RevokeComponentKeys(ctx, component)
}
func (e *errComponentStore) CountActiveComponentKeys(ctx context.Context, component string) (int64, error) {
	return e.inner.CountActiveComponentKeys(ctx, component)
}
func (e *errComponentStore) DeleteComponentWithRevoke(ctx context.Context, name string) (int64, error) {
	return e.inner.DeleteComponentWithRevoke(ctx, name)
}
func (e *errComponentStore) UpdateComponentVisibility(ctx context.Context, name, vis string) (*store.Component, error) {
	return e.inner.UpdateComponentVisibility(ctx, name, vis)
}
