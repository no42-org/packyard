/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/no42-org/packyard-auth/internal/metrics"
)

// countingComponentStore is an in-memory ComponentStore that counts
// GetComponent calls, can be switched to return an error, and can block
// GetComponent on a gate channel so tests can pile up concurrent callers.
type countingComponentStore struct {
	mu        sync.Mutex
	comps     map[string]*Component
	getCalls  int
	err       error // returned by GetComponent when non-nil
	updateErr error // returned by UpdateComponentVisibility when non-nil
	gate      chan struct{}
}

func newCountingStore(comps ...*Component) *countingComponentStore {
	s := &countingComponentStore{comps: map[string]*Component{}}
	for _, c := range comps {
		s.comps[c.Name] = c
	}
	return s
}

func (s *countingComponentStore) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getCalls
}

func (s *countingComponentStore) setErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

func (s *countingComponentStore) GetComponent(_ context.Context, name string) (*Component, error) {
	s.mu.Lock()
	s.getCalls++
	err, gate := s.err, s.gate
	c, ok := s.comps[name]
	s.mu.Unlock()
	if gate != nil {
		<-gate
	}
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrComponentNotFound
	}
	cp := *c
	return &cp, nil
}

func (s *countingComponentStore) UpdateComponentVisibility(_ context.Context, name, visibility string) (*Component, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.updateErr != nil {
		return nil, s.updateErr
	}
	c, ok := s.comps[name]
	if !ok {
		return nil, ErrComponentNotFound
	}
	c.Visibility = visibility
	cp := *c
	return &cp, nil
}

func (s *countingComponentStore) DeleteComponent(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.comps[name]; !ok {
		return ErrComponentNotFound
	}
	delete(s.comps, name)
	return nil
}

func (s *countingComponentStore) DeleteComponentWithRevoke(ctx context.Context, name string) (int64, error) {
	return 0, s.DeleteComponent(ctx, name)
}

func (s *countingComponentStore) CreateComponent(_ context.Context, c *Component) (*Component, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.comps[c.Name] = c
	return c, nil
}
func (s *countingComponentStore) ListComponents(context.Context, int, int) ([]*Component, error) {
	return nil, nil
}
func (s *countingComponentStore) RevokeComponentKeys(context.Context, string) (int64, error) {
	return 0, nil
}
func (s *countingComponentStore) CountActiveComponentKeys(context.Context, string) (int64, error) {
	return 0, nil
}

const testTTL = 30 * time.Second

func public(name string) *Component  { return &Component{Name: name, Visibility: "public"} }
func private(name string) *Component { return &Component{Name: name, Visibility: "private"} }

func mustGet(t *testing.T, cs ComponentStore, name string) *Component {
	t.Helper()
	c, err := cs.GetComponent(context.Background(), name)
	if err != nil {
		t.Fatalf("GetComponent(%q): %v", name, err)
	}
	return c
}

func TestCached_HitAvoidsInner(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inner := newCountingStore(public("core"))
		cs := NewCachedComponentStore(context.Background(), inner, testTTL)
		hitsBefore := testutil.ToFloat64(metrics.ComponentCacheHits)

		for i := 0; i < 3; i++ {
			if c := mustGet(t, cs, "core"); c.Visibility != "public" {
				t.Fatalf("visibility = %q", c.Visibility)
			}
		}
		if inner.calls() != 1 {
			t.Fatalf("inner calls = %d, want 1", inner.calls())
		}
		if got := testutil.ToFloat64(metrics.ComponentCacheHits) - hitsBefore; got != 2 {
			t.Fatalf("hits delta = %v, want 2", got)
		}
	})
}

func TestCached_PrivateAndNotFoundNeverCached(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inner := newCountingStore(private("secret"))
		cs := NewCachedComponentStore(context.Background(), inner, testTTL)

		for i := 0; i < 2; i++ {
			if c := mustGet(t, cs, "secret"); c.Visibility != "private" {
				t.Fatalf("visibility = %q", c.Visibility)
			}
		}
		for i := 0; i < 2; i++ {
			if _, err := cs.GetComponent(context.Background(), "ghost"); !errors.Is(err, ErrComponentNotFound) {
				t.Fatalf("ghost err = %v, want ErrComponentNotFound", err)
			}
		}
		if inner.calls() != 4 {
			t.Fatalf("inner calls = %d, want 4 (nothing cached)", inner.calls())
		}
		if n := cs.Len(); n != 0 {
			t.Fatalf("cache entries = %d, want 0", n)
		}
	})
}

func TestCached_TTLExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inner := newCountingStore(public("core"))
		cs := NewCachedComponentStore(context.Background(), inner, testTTL)

		mustGet(t, cs, "core")
		time.Sleep(testTTL - time.Nanosecond)
		mustGet(t, cs, "core")
		if inner.calls() != 1 {
			t.Fatalf("inner calls before expiry = %d, want 1", inner.calls())
		}
		time.Sleep(2 * time.Nanosecond)
		mustGet(t, cs, "core")
		if inner.calls() != 2 {
			t.Fatalf("inner calls after expiry = %d, want 2", inner.calls())
		}
	})
}

func TestCached_SurvivesInnerErrorWithinTTL_FailsClosedAfter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inner := newCountingStore(public("core"))
		cs := NewCachedComponentStore(context.Background(), inner, testTTL)

		mustGet(t, cs, "core")
		inner.setErr(errors.New("database is locked"))

		// Within TTL: served from cache despite the outage.
		if c := mustGet(t, cs, "core"); c.Visibility != "public" {
			t.Fatalf("visibility during outage = %q", c.Visibility)
		}
		// Unseen component during the outage: error propagates, nothing cached.
		if _, err := cs.GetComponent(context.Background(), "other"); err == nil {
			t.Fatal("expected error for uncached component during outage")
		}
		// After TTL: fail closed.
		time.Sleep(testTTL + time.Nanosecond)
		if _, err := cs.GetComponent(context.Background(), "core"); err == nil {
			t.Fatal("expected error after TTL during outage")
		}
		if n := cs.Len(); n != 0 {
			t.Fatalf("cache entries after failed refresh = %d, want 0", n)
		}
	})
}

func TestCached_PublicToPrivateEvictsImmediately(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inner := newCountingStore(public("core"))
		cs := NewCachedComponentStore(context.Background(), inner, testTTL)

		mustGet(t, cs, "core")
		if _, err := cs.UpdateComponentVisibility(context.Background(), "core", "private"); err != nil {
			t.Fatal(err)
		}
		c := mustGet(t, cs, "core")
		if c.Visibility != "private" {
			t.Fatalf("visibility after flip = %q, want private", c.Visibility)
		}
		if inner.calls() != 2 {
			t.Fatalf("inner calls = %d, want 2 (evicted then re-read)", inner.calls())
		}
	})
}

func TestCached_DeleteEvicts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inner := newCountingStore(public("a"), public("b"))
		cs := NewCachedComponentStore(context.Background(), inner, testTTL)
		mustGet(t, cs, "a")
		mustGet(t, cs, "b")

		if err := cs.DeleteComponent(context.Background(), "a"); err != nil {
			t.Fatal(err)
		}
		if _, err := cs.DeleteComponentWithRevoke(context.Background(), "b"); err != nil {
			t.Fatal(err)
		}
		if n := cs.Len(); n != 0 {
			t.Fatalf("entries after deletes = %d, want 0", n)
		}
		if _, err := cs.GetComponent(context.Background(), "a"); !errors.Is(err, ErrComponentNotFound) {
			t.Fatalf("a after delete: %v", err)
		}
	})
}

func TestCached_FailedWriteDoesNotEvict(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inner := newCountingStore(public("core"))
		cs := NewCachedComponentStore(context.Background(), inner, testTTL)
		mustGet(t, cs, "core")

		inner.updateErr = errors.New("disk full")
		if _, err := cs.UpdateComponentVisibility(context.Background(), "core", "private"); err == nil {
			t.Fatal("expected write error")
		}
		mustGet(t, cs, "core")
		if inner.calls() != 1 {
			t.Fatalf("inner calls = %d, want 1 (entry must survive a failed write)", inner.calls())
		}
	})
}

func TestCached_StampedeCollapsesToOneInnerCall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inner := newCountingStore(public("core"))
		inner.gate = make(chan struct{})
		cs := NewCachedComponentStore(context.Background(), inner, testTTL)

		const n = 25
		var wg sync.WaitGroup
		results := make([]error, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, results[i] = cs.GetComponent(context.Background(), "core")
			}(i)
		}
		synctest.Wait() // every caller is now blocked behind the single in-flight lookup
		close(inner.gate)
		wg.Wait()

		for i, err := range results {
			if err != nil {
				t.Fatalf("caller %d: %v", i, err)
			}
		}
		if inner.calls() != 1 {
			t.Fatalf("inner calls = %d, want 1", inner.calls())
		}
	})
}

func TestCached_ReturnsCopies(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inner := newCountingStore(public("core"))
		cs := NewCachedComponentStore(context.Background(), inner, testTTL)

		first := mustGet(t, cs, "core")
		first.Visibility = "mutated"
		second := mustGet(t, cs, "core")
		if second.Visibility != "public" {
			t.Fatalf("cached entry was mutated through a returned pointer: %q", second.Visibility)
		}
	})
}

func TestCached_DisabledPassesThrough(t *testing.T) {
	inner := newCountingStore(public("core"))
	cs := NewCachedComponentStore(context.Background(), inner, 0)
	for i := 0; i < 3; i++ {
		mustGet(t, cs, "core")
	}
	if inner.calls() != 3 {
		t.Fatalf("inner calls = %d, want 3 with cache disabled", inner.calls())
	}
	if n := cs.Len(); n != 0 {
		t.Fatalf("entries = %d, want 0 with cache disabled", n)
	}
}

func TestCached_InnerCallUsesServiceContextNotCallers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inner := newCountingStore(public("core"))
		cs := NewCachedComponentStore(context.Background(), inner, testTTL)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // an already-cancelled request context must not fail the lookup
		if _, err := cs.GetComponent(ctx, "core"); err != nil {
			t.Fatalf("lookup with cancelled caller context: %v", err)
		}
	})
}
