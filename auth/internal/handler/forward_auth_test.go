/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

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
	createKeyFn      func(ctx context.Context, accountID, component, label string, expiresAt *time.Time) (*store.Key, error)
	listKeysFn       func(ctx context.Context, componentFilter, accountFilter string, offset, limit int) ([]*store.Key, error)
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

func (m *mockStore) CreateKey(ctx context.Context, accountID, component, label string, expiresAt *time.Time) (*store.Key, error) {
	if m.createKeyFn != nil {
		return m.createKeyFn(ctx, accountID, component, label, expiresAt)
	}
	return nil, errors.New("not implemented")
}

func (m *mockStore) ListKeys(ctx context.Context, componentFilter, accountFilter string, offset, limit int) ([]*store.Key, error) {
	if m.listKeysFn != nil {
		return m.listKeysFn(ctx, componentFilter, accountFilter, offset, limit)
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
	h := NewForwardAuthHandler(&mockStore{}, newTestComponentStore(), &fwdAuthStubAccountStore{}, slog.Default())
	if h.Store == nil {
		t.Error("Store should not be nil")
	}
	if h.ComponentStore == nil {
		t.Error("ComponentStore should not be nil")
	}
	if h.AccountStore == nil {
		t.Error("AccountStore should not be nil")
	}
}

func TestNewForwardAuthHandler_NilAccountStorePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when AccountStore is nil; got none")
		}
	}()
	_ = NewForwardAuthHandler(&mockStore{}, newTestComponentStore(), nil, slog.Default())
}

// fwdAuthStubAccountStore is a minimal AccountStore for forward-auth tests
// that only need the gate constructed; methods panic if unexpectedly called.
type fwdAuthStubAccountStore struct{}

func (fwdAuthStubAccountStore) CreateAccount(context.Context, store.AccountInput, string) (*store.Account, error) {
	panic("unexpected")
}
func (fwdAuthStubAccountStore) GetAccount(context.Context, string) (*store.Account, error) {
	return &store.Account{Status: store.AccountStatusActive}, nil
}
func (fwdAuthStubAccountStore) ListAccounts(context.Context, store.AccountStatus, int, int) ([]*store.Account, error) {
	panic("unexpected")
}
func (fwdAuthStubAccountStore) UpdateAccount(context.Context, string, store.AccountUpdate) (*store.Account, error) {
	panic("unexpected")
}
func (fwdAuthStubAccountStore) DeleteAccountWithCascade(context.Context, string) (int64, error) {
	panic("unexpected")
}
func (fwdAuthStubAccountStore) CountActiveAccountKeys(context.Context, string) (int64, error) {
	panic("unexpected")
}
func (fwdAuthStubAccountStore) ListAccountKeys(context.Context, string, int, int) ([]*store.Key, error) {
	panic("unexpected")
}
func (fwdAuthStubAccountStore) CreateKeyForAccount(context.Context, string, string, string, *time.Time) (*store.Key, error) {
	panic("unexpected")
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

func newMixedVisibilityHandler(keyStore store.KeyStore) *ForwardAuthHandler {
	cs := newStubComponentStore()
	cs.comps["core"] = &store.Component{Name: "core", Visibility: "public"}
	cs.comps["minion"] = &store.Component{Name: "minion", Visibility: "private"}
	return &ForwardAuthHandler{Store: keyStore, ComponentStore: cs, Logger: slog.Default()}
}

func TestForwardAuth_PublicComponent_NoCreds(t *testing.T) {
	h := newMixedVisibilityHandler(&mockStore{})
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
	h := newMixedVisibilityHandler(&mockStore{
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
	h := newMixedVisibilityHandler(&mockStore{
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
	h := newMixedVisibilityHandler(&mockStore{})
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
func (e *errComponentStore) ListComponents(ctx context.Context, offset, limit int) ([]*store.Component, error) {
	return e.inner.ListComponents(ctx, offset, limit)
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

// FuzzExtractComponent checks the invariants extractComponent must hold for
// any request path: a returned component is never empty, never contains a
// separator, and always appears verbatim in the input.
func FuzzExtractComponent(f *testing.F) {
	for _, seed := range []string{
		"/rpm/core/2025/el9-x86_64/Packages/x.rpm",
		"/deb/core/2025/dists/bookworm/InRelease",
		"/oci/v2/lts-core/manifests/2025",
		"/oci/v2/core/manifests/2025",
		"/gpg/lts.asc",
		"/",
		"",
		"rpm//x",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, path string) {
		comp, ok := extractComponent(path)
		if !ok {
			if comp != "" {
				t.Fatalf("extractComponent(%q) = (%q, false); component must be empty on failure", path, comp)
			}
			return
		}
		if comp == "" {
			t.Fatalf("extractComponent(%q) returned ok with empty component", path)
		}
		if strings.Contains(comp, "/") {
			t.Fatalf("extractComponent(%q) = %q contains a separator", path, comp)
		}
		if !strings.Contains(path, comp) {
			t.Fatalf("extractComponent(%q) = %q not present in input", path, comp)
		}
	})
}

// toggleErrComponentStore wraps a stubComponentStore and, once failing is set,
// returns an error from GetComponent for every name. It simulates a database
// outage that begins after the cache has been warmed.
type toggleErrComponentStore struct {
	*stubComponentStore
	failing bool
}

func (s *toggleErrComponentStore) GetComponent(ctx context.Context, name string) (*store.Component, error) {
	if s.failing {
		return nil, errors.New("database is locked")
	}
	return s.stubComponentStore.GetComponent(ctx, name)
}

// TestForwardAuth_PublicComponentCache_SurvivesOutage covers issue #84 AC1 at
// the handler level: with the CachedComponentStore in front of a store that
// starts failing, a recently seen public component keeps returning 200 while an
// unseen component fails closed with 503. With the cache disabled the same
// sequence returns 503 for both, which is today's behaviour.
func TestForwardAuth_PublicComponentCache_SurvivesOutage(t *testing.T) {
	run := func(t *testing.T, ttl time.Duration, wantCached int) {
		cs := newStubComponentStore()
		cs.comps["core"] = &store.Component{Name: "core", Visibility: "public"}
		cs.comps["minion"] = &store.Component{Name: "minion", Visibility: "private"}
		toggle := &toggleErrComponentStore{stubComponentStore: cs}
		h := &ForwardAuthHandler{
			Store:          &mockStore{},
			ComponentStore: store.NewCachedComponentStore(context.Background(), toggle, ttl),
			Logger:         slog.Default(),
		}
		get := func(uri string) int {
			req := httptest.NewRequest("GET", "/auth", nil)
			req.Header.Set("X-Forwarded-Uri", uri)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			return w.Code
		}

		if code := get("/rpm/core/2025/el9-x86_64/"); code != http.StatusOK {
			t.Fatalf("warm-up: expected 200, got %d", code)
		}
		// Warm the private component too: it must be looked up and denied, and
		// must NOT be cached, so it fails closed once the store is down.
		if code := get("/rpm/minion/2025/el9-x86_64/"); code != http.StatusUnauthorized {
			t.Fatalf("warm-up private without creds: expected 401, got %d", code)
		}
		toggle.failing = true
		if code := get("/rpm/core/2025/el9-x86_64/"); code != wantCached {
			t.Fatalf("cached public component during outage: expected %d, got %d", wantCached, code)
		}
		if code := get("/rpm/minion/2025/el9-x86_64/"); code != http.StatusServiceUnavailable {
			t.Fatalf("uncached component during outage: expected 503, got %d", code)
		}
	}
	t.Run("cache enabled serves public from cache", func(t *testing.T) {
		run(t, 30*time.Second, http.StatusOK)
	})
	t.Run("cache disabled fails closed", func(t *testing.T) {
		run(t, 0, http.StatusServiceUnavailable)
	})
}
