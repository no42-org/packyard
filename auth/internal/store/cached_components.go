/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

package store

import (
	"context"
	"slices"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/no42-org/packyard-auth/internal/metrics"
)

// lookupTimeout bounds the shared store call behind a cache miss. It matches
// the SQLite busy_timeout so a hung database cannot pin waiters for longer
// than the store itself would.
const lookupTimeout = 5 * time.Second

// CachedComponentStore decorates a ComponentStore with a positive-only,
// hard-TTL cache of components whose visibility is "public".
//
// Design (issue #84):
//   - Only "public" results are cached. "private", ErrComponentNotFound and
//     store errors are never cached, so the set is bounded by the number of
//     public components and a database outage never widens access.
//   - Entries expire after ttl and are removed lazily on the next lookup.
//     There is no stale-while-revalidate: once expired, an entry is gone and
//     a lookup during an outage fails closed exactly as it did before #84.
//   - Mutating calls (visibility update, delete) pass through and evict the
//     name on success only, so a public→private change is immediate on this
//     instance and bounded by ttl on any other. Eviction also bumps a
//     per-name generation so a lookup that was already in flight when the
//     write landed cannot re-insert the pre-write value.
//   - Concurrent misses for one name are collapsed by singleflight into a
//     single store call that runs on a service-owned context, not on the
//     first caller's request context.
//
// A ttl <= 0 disables the cache: every method passes straight through.
type CachedComponentStore struct {
	ComponentStore

	base context.Context
	ttl  time.Duration

	mu      sync.RWMutex
	entries map[string]cachedComponent
	gen     map[string]uint64 // bumped on every eviction caused by a write
	sf      singleflight.Group
}

type cachedComponent struct {
	comp      Component
	expiresAt time.Time
}

// NewCachedComponentStore wraps inner. base is the service lifetime context
// used for shared store lookups; it should be cancelled on shutdown.
func NewCachedComponentStore(base context.Context, inner ComponentStore, ttl time.Duration) *CachedComponentStore {
	return &CachedComponentStore{
		ComponentStore: inner,
		base:           base,
		ttl:            ttl,
		entries:        map[string]cachedComponent{},
		gen:            map[string]uint64{},
	}
}

// Len returns the number of cached entries, expired or not. Intended for tests
// and metrics; the gauge is updated on insert and evict.
func (c *CachedComponentStore) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

func (c *CachedComponentStore) enabled() bool { return c.ttl > 0 }

// cloneComponent returns a deep copy so callers can never reach the cached
// slices through a returned value.
func cloneComponent(src Component) Component {
	src.RPMSeries = slices.Clone(src.RPMSeries)
	src.RPMOSFamilies = slices.Clone(src.RPMOSFamilies)
	src.RPMArchitectures = slices.Clone(src.RPMArchitectures)
	return src
}

// GetComponent returns a copy of the cached component on a fresh hit, and
// otherwise performs one shared lookup against the inner store.
func (c *CachedComponentStore) GetComponent(ctx context.Context, name string) (*Component, error) {
	metrics.ComponentCacheRequests.Inc()
	if !c.enabled() {
		return c.ComponentStore.GetComponent(ctx, name)
	}

	if comp, ok := c.lookup(name); ok {
		metrics.ComponentCacheHits.Inc()
		return &comp, nil
	}

	v, err, _ := c.sf.Do(name, func() (any, error) {
		// Capture the generation before the read. If a write evicts this name
		// while the read is in flight, the generation moves on and the stale
		// result is not inserted.
		gen := c.generation(name)
		lctx, cancel := context.WithTimeout(c.base, lookupTimeout)
		defer cancel()
		comp, err := c.ComponentStore.GetComponent(lctx, name)
		if err != nil {
			return nil, err
		}
		if comp.Visibility == "public" {
			c.insert(name, *comp, gen)
		}
		return comp, nil
	})
	if err != nil {
		return nil, err
	}
	cp := cloneComponent(*v.(*Component))
	return &cp, nil
}

func (c *CachedComponentStore) generation(name string) uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.gen[name]
}

// lookup returns a copy of a non-expired entry. An expired entry is removed
// only if it is still the same entry that was found expired, so a concurrent
// fresher insert is never discarded.
func (c *CachedComponentStore) lookup(name string) (Component, bool) {
	c.mu.RLock()
	e, ok := c.entries[name]
	c.mu.RUnlock()
	if !ok {
		return Component{}, false
	}
	if time.Now().Before(e.expiresAt) {
		return cloneComponent(e.comp), true
	}
	c.mu.Lock()
	if cur, ok := c.entries[name]; ok && cur.expiresAt.Equal(e.expiresAt) {
		delete(c.entries, name)
		metrics.ComponentCacheEntries.Set(float64(len(c.entries)))
	}
	c.mu.Unlock()
	return Component{}, false
}

// insert stores comp unless the name's generation changed since gen was read.
func (c *CachedComponentStore) insert(name string, comp Component, gen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gen[name] != gen {
		return
	}
	c.entries[name] = cachedComponent{comp: cloneComponent(comp), expiresAt: time.Now().Add(c.ttl)}
	metrics.ComponentCacheEntries.Set(float64(len(c.entries)))
}

// evict removes the name and bumps its generation so in-flight lookups that
// started before the write cannot re-insert their result.
func (c *CachedComponentStore) evict(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, name)
	c.gen[name]++
	metrics.ComponentCacheEntries.Set(float64(len(c.entries)))
}

// UpdateComponentVisibility passes through and evicts the name on success.
func (c *CachedComponentStore) UpdateComponentVisibility(ctx context.Context, name, visibility string) (*Component, error) {
	comp, err := c.ComponentStore.UpdateComponentVisibility(ctx, name, visibility)
	if err == nil && c.enabled() {
		c.evict(name)
	}
	return comp, err
}

// DeleteComponent passes through and evicts the name on success.
func (c *CachedComponentStore) DeleteComponent(ctx context.Context, name string) error {
	err := c.ComponentStore.DeleteComponent(ctx, name)
	if err == nil && c.enabled() {
		c.evict(name)
	}
	return err
}

// DeleteComponentWithRevoke passes through and evicts the name on success.
func (c *CachedComponentStore) DeleteComponentWithRevoke(ctx context.Context, name string) (int64, error) {
	n, err := c.ComponentStore.DeleteComponentWithRevoke(ctx, name)
	if err == nil && c.enabled() {
		c.evict(name)
	}
	return n, err
}
