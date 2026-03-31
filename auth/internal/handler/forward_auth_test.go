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

	"github.com/opennms/packyard-auth/internal/store"
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

func newTestHandler(s store.KeyStore) *ForwardAuthHandler {
	return &ForwardAuthHandler{
		Store:  s,
		Logger: slog.Default(),
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
	req.Header.Set("X-Forwarded-Uri", "/rpm/el9-x86_64/core/2025/")
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
	req.Header.Set("X-Forwarded-Uri", "/rpm/el9-x86_64/core/2025/meridian-core-2025.rpm")
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
	req.Header.Set("X-Forwarded-Uri", "/rpm/el9-x86_64/core/2025/")
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
	req.Header.Set("X-Forwarded-Uri", "/rpm/el9-x86_64/core/2025/")
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
	req.Header.Set("X-Forwarded-Uri", "/rpm/el9-x86_64/core/2025/")
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
	req.Header.Set("X-Forwarded-Uri", "/rpm/el9-x86_64/core/2025/")
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
	req.Header.Set("X-Forwarded-Uri", "/rpm/el9-x86_64/core/2025/")
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
	cases := []string{"", "/", "/gpg/meridian.asc", "/unknown/path"}
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
	req.Header.Set("X-Forwarded-Uri", "/rpm/el9-x86_64/core/2025/")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("expected empty body, got %q", w.Body.String())
	}
}
