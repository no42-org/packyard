/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemStateStore_PutConsumeHappyPath(t *testing.T) {
	s := NewMemStateStore(context.Background(), time.Hour)
	s.Put("h1", StateEntry{State: "abc", CodeVerifier: "v", Provider: "github"})
	got, err := s.Consume("h1")
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if got.State != "abc" || got.CodeVerifier != "v" || got.Provider != "github" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestMemStateStore_ConsumeIsSingleUse(t *testing.T) {
	s := NewMemStateStore(context.Background(), time.Hour)
	s.Put("h1", StateEntry{State: "abc"})
	if _, err := s.Consume("h1"); err != nil {
		t.Fatalf("first Consume: %v", err)
	}
	_, err := s.Consume("h1")
	if !errors.Is(err, ErrStateNotFound) {
		t.Errorf("second Consume should be NotFound, got %v", err)
	}
}

func TestMemStateStore_ConsumeUnknownHandle(t *testing.T) {
	s := NewMemStateStore(context.Background(), time.Hour)
	_, err := s.Consume("nope")
	if !errors.Is(err, ErrStateNotFound) {
		t.Errorf("want ErrStateNotFound, got %v", err)
	}
}

func TestMemStateStore_ExpiredEntryRejected(t *testing.T) {
	s := NewMemStateStore(context.Background(), time.Hour)
	fakeNow := time.Now()
	s.WithClock(func() time.Time { return fakeNow })
	s.Put("h1", StateEntry{State: "abc", Created: fakeNow.Add(-StateTTL).Add(-time.Second)})

	_, err := s.Consume("h1")
	if !errors.Is(err, ErrStateNotFound) {
		t.Errorf("expired entry: want ErrStateNotFound, got %v", err)
	}
}

func TestPKCECodeChallenge_RFC7636Vector(t *testing.T) {
	// RFC 7636 appendix B test vector:
	//   verifier  = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	//   challenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	got := PKCECodeChallenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk")
	want := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if got != want {
		t.Errorf("RFC 7636 vector mismatch:\n  got  %q\n  want %q", got, want)
	}
}

func TestRandomHex_LengthAndDistinctness(t *testing.T) {
	a, err := RandomHex(16)
	if err != nil {
		t.Fatalf("RandomHex: %v", err)
	}
	if len(a) != 32 {
		t.Errorf("16-byte hex should be 32 chars, got %d", len(a))
	}
	b, _ := RandomHex(16)
	if a == b {
		t.Error("two RandomHex calls returned the same value")
	}
}
