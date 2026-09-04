/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// StateTTL is the lifetime of a stored OAuth state + PKCE verifier per D18
// of change 2026-05-21-admin-ui-account-lifecycle.
const StateTTL = 15 * time.Minute

// ErrStateNotFound is returned by StateStore.Consume when the handle does not
// match a live entry. Maps to 400 INVALID_OAUTH_STATE at the callback.
var ErrStateNotFound = errors.New("oauth state not found or expired")

// StateEntry holds the per-flow values stored under an opaque handle. State
// and CodeVerifier are random per-flow; Provider is the chosen provider
// name; Created is used by the reaper.
type StateEntry struct {
	State        string
	CodeVerifier string
	Provider     string
	Created      time.Time
}

// StateStore is the contract the login handler uses to stash a flow's state +
// PKCE verifier under an opaque handle, and the callback uses to consume it
// exactly once. The in-process implementation is sufficient for a
// single-replica admin service; replication would require a shared backend.
type StateStore interface {
	Put(handle string, entry StateEntry)
	Consume(handle string) (StateEntry, error)
}

// memStateStore is an in-process StateStore keyed by handle. A reaper
// goroutine evicts entries older than StateTTL on a 5-minute cadence so the
// map does not grow unbounded under high failed-flow churn. The reaper is
// auto-started by NewMemStateStore so embedders can't forget to wire it.
type memStateStore struct {
	mu      sync.Mutex
	entries map[string]StateEntry
	now     func() time.Time
}

// NewMemStateStore returns a StateStore backed by an in-memory map. The
// reaper goroutine is started immediately and runs until ctx is cancelled —
// production passes the server-lifetime context; tests pass
// context.Background() (the goroutine leaks at process exit, which is fine).
//
// reapInterval controls how often the reaper runs. Pass zero to default to
// 5 minutes (matches production cadence). Consume's TTL check guarantees
// correctness regardless of reaper cadence; the reaper is only a memory
// bound for failed-flow churn.
func NewMemStateStore(ctx context.Context, reapInterval time.Duration) *memStateStore {
	if reapInterval <= 0 {
		reapInterval = 5 * time.Minute
	}
	s := &memStateStore{
		entries: make(map[string]StateEntry),
		now:     time.Now,
	}
	go s.reapLoop(ctx, reapInterval)
	return s
}

func (s *memStateStore) reapLoop(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			s.reap()
		case <-ctx.Done():
			return
		}
	}
}

func (s *memStateStore) reap() {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := s.now().Add(-StateTTL)
	for k, v := range s.entries {
		if v.Created.Before(cutoff) {
			delete(s.entries, k)
		}
	}
}

// WithClock swaps the time source for tests. Must be called BEFORE any
// concurrent Put/Consume/reap, otherwise the assignment races with reader
// goroutines. Tests typically call it right after NewMemStateStore.
func (s *memStateStore) WithClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

func (s *memStateStore) Put(handle string, entry StateEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry.Created.IsZero() {
		entry.Created = s.now()
	}
	s.entries[handle] = entry
}

// Consume returns the entry under handle and removes it. Returns
// ErrStateNotFound if the handle does not match or the entry has aged past
// StateTTL — both cases collapse to the same error so a caller cannot
// distinguish "unknown handle" from "expired entry" via timing.
func (s *memStateStore) Consume(handle string) (StateEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[handle]
	if !ok {
		return StateEntry{}, ErrStateNotFound
	}
	delete(s.entries, handle)
	if s.now().Sub(e.Created) > StateTTL {
		return StateEntry{}, ErrStateNotFound
	}
	return e, nil
}

// ─── PKCE + state helpers ──────────────────────────────────────────────────

// RandomHex returns n random bytes hex-encoded. Used for state, handle, and
// the PKCE code_verifier (which is then transformed via S256 into the
// code_challenge presented to the provider).
func RandomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// PKCECodeChallenge derives the S256 code_challenge from a code_verifier per
// RFC 7636 §4.2. The provider sees only the challenge; the verifier is
// retained server-side and presented at token exchange.
func PKCECodeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
