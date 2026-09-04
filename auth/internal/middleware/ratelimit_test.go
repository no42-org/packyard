/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

package middleware

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/no42-org/packyard-auth/internal/audit"
)

// recordingAuditor captures audit entries for assertion.
type recordingAuditor struct{ entries []audit.Entry }

func (r *recordingAuditor) Write(_ context.Context, e audit.Entry) {
	r.entries = append(r.entries, e)
}

func newTestRateLimiter(t *testing.T) (*RateLimiter, *recordingAuditor) {
	t.Helper()
	rec := &recordingAuditor{}
	// Cancellable ctx + Cleanup so the reaper goroutine doesn't leak
	// across `go test -count=N` runs.
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	rl := NewRateLimiter(ctx, RateLimiterConfig{
		Capacity:        3, // small for tests
		RefillPerSecond: 1.0 / 6.0,
		Auditor:         rec,
		Logger:          slog.Default(),
	})
	return rl, rec
}

func runRateLimited(rl *RateLimiter, ip string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/login/github", nil)
	req.RemoteAddr = ip
	rec := httptest.NewRecorder()
	rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, req)
	return rec
}

func TestRateLimiter_AllowsUpToCapacity(t *testing.T) {
	rl, _ := newTestRateLimiter(t)
	for i := 0; i < 3; i++ {
		rec := runRateLimited(rl, "1.2.3.4:1234")
		if rec.Code != http.StatusOK {
			t.Errorf("request %d should be allowed, got %d", i+1, rec.Code)
		}
	}
}

func TestRateLimiter_BlocksWhenExhausted(t *testing.T) {
	rl, rec := newTestRateLimiter(t)
	for i := 0; i < 3; i++ {
		runRateLimited(rl, "1.2.3.4:1234")
	}
	resp := runRateLimited(rl, "1.2.3.4:1234")
	if resp.Code != http.StatusTooManyRequests {
		t.Fatalf("4th request: want 429, got %d", resp.Code)
	}
	var body struct{ Code string }
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Code != "RATE_LIMITED" {
		t.Errorf("want code RATE_LIMITED, got %q", body.Code)
	}
	if len(rec.entries) != 1 || rec.entries[0].Action != "auth.rate_limited" {
		t.Errorf("want one auth.rate_limited audit row, got %+v", rec.entries)
	}
}

func TestRateLimiter_DistinctIPsHaveIndependentBuckets(t *testing.T) {
	rl, _ := newTestRateLimiter(t)
	for i := 0; i < 3; i++ {
		runRateLimited(rl, "1.1.1.1:1234")
	}
	// IP #1 is now empty; IP #2 should still pass.
	if rec := runRateLimited(rl, "2.2.2.2:1234"); rec.Code != http.StatusOK {
		t.Errorf("distinct IP should pass, got %d", rec.Code)
	}
	if rec := runRateLimited(rl, "1.1.1.1:1234"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("original IP still throttled, got %d", rec.Code)
	}
}

func TestRateLimiter_RefillsOverTime(t *testing.T) {
	rl, _ := newTestRateLimiter(t)
	t0 := time.Now()
	rl.WithClock(func() time.Time { return t0 })

	// Drain bucket.
	for i := 0; i < 3; i++ {
		runRateLimited(rl, "1.2.3.4:1")
	}
	if rec := runRateLimited(rl, "1.2.3.4:1"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected throttle, got %d", rec.Code)
	}

	// Advance the clock by 12 seconds → 2 tokens refilled.
	rl.WithClock(func() time.Time { return t0.Add(12 * time.Second) })
	for i := 0; i < 2; i++ {
		if rec := runRateLimited(rl, "1.2.3.4:1"); rec.Code != http.StatusOK {
			t.Errorf("post-refill request %d should pass, got %d", i+1, rec.Code)
		}
	}
	if rec := runRateLimited(rl, "1.2.3.4:1"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("3rd post-refill request should be throttled, got %d", rec.Code)
	}
}

// TestRateLimiter_DefaultsMatchSpec asserts the spec-mandated D21 values
// can't drift silently. Capacity 10, refill 1 token per 6 seconds.
func TestRateLimiter_DefaultsMatchSpec(t *testing.T) {
	if DefaultRateLimitCapacity != 10 {
		t.Errorf("DefaultRateLimitCapacity: spec says 10, got %d", DefaultRateLimitCapacity)
	}
	want := 1.0 / 6.0
	if DefaultRateLimitRefillPerS != want {
		t.Errorf("DefaultRateLimitRefillPerS: spec says %v, got %v", want, DefaultRateLimitRefillPerS)
	}
}

// TestRateLimiter_CoalescesAuditUnderSustainedAttack — bucket throttled for
// many seconds emits ONE audit row, not one per request.
func TestRateLimiter_CoalescesAuditUnderSustainedAttack(t *testing.T) {
	rl, rec := newTestRateLimiter(t)
	t0 := time.Now()
	rl.WithClock(func() time.Time { return t0 })

	// Drain bucket: 3 ok, then a flood of 50 over-cap requests.
	for i := 0; i < 3; i++ {
		runRateLimited(rl, "1.2.3.4:1")
	}
	for i := 0; i < 50; i++ {
		runRateLimited(rl, "1.2.3.4:1")
	}
	if len(rec.entries) != 1 {
		t.Errorf("audit coalesce: want 1 row for sustained attack, got %d", len(rec.entries))
	}

	// Advance past the coalesce window — bucket refills to capacity (cap=3
	// in tests). Drain capacity + 1 to trigger a second audited throttle.
	rl.WithClock(func() time.Time { return t0.Add(auditCoalesceWindow + time.Second) })
	for i := 0; i < 3; i++ {
		runRateLimited(rl, "1.2.3.4:1")
	}
	runRateLimited(rl, "1.2.3.4:1") // throttled, new audit window
	if len(rec.entries) != 2 {
		t.Errorf("post-window audit: want 2 total rows, got %d", len(rec.entries))
	}
}

// TestRateLimiter_AuditEntryIncludesIPAndTruncatedPath — verifies the
// `details.ip` spec compliance and the path-length cap.
func TestRateLimiter_AuditEntryIncludesIPAndTruncatedPath(t *testing.T) {
	rl, rec := newTestRateLimiter(t)
	for i := 0; i < 3; i++ {
		runRateLimited(rl, "1.2.3.4:1")
	}

	// 4th request triggers audit. Use a long path.
	longPath := "/api/v1/auth/login/" + strings.Repeat("x", 1000)
	req := httptest.NewRequest(http.MethodGet, longPath, nil)
	req.RemoteAddr = "1.2.3.4:1"
	w := httptest.NewRecorder()
	rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(w, req)

	if len(rec.entries) != 1 {
		t.Fatalf("want 1 audit row, got %d", len(rec.entries))
	}
	details := rec.entries[0].Details
	if details["ip"] != "1.2.3.4" {
		t.Errorf("details.ip: want 1.2.3.4 (port stripped), got %v", details["ip"])
	}
	path, _ := details["path"].(string)
	if len(path) > auditPathMaxLen+4 { // +4 for the ellipsis rune
		t.Errorf("path should be truncated to ~%d chars, got %d", auditPathMaxLen, len(path))
	}
}

// TestRateLimiter_ClockRollbackFloorsTokensAtZero — backward NTP step must
// not push tokens negative; recovery from rollback uses forward refill only.
func TestRateLimiter_ClockRollbackFloorsTokensAtZero(t *testing.T) {
	rl, _ := newTestRateLimiter(t)
	t0 := time.Now()
	rl.WithClock(func() time.Time { return t0 })

	// Drain bucket.
	for i := 0; i < 3; i++ {
		runRateLimited(rl, "1.2.3.4:1")
	}

	// Clock rolls back 1 hour. Without the floor, tokens would be ~-600
	// after the refill calculation, and the IP would be blackholed until
	// ~600 forward seconds of refill caught up. With the floor at zero, the
	// bucket recovers normally to capacity once enough time passes.
	rl.WithClock(func() time.Time { return t0.Add(-time.Hour) })
	runRateLimited(rl, "1.2.3.4:1") // touches the bucket — floor kicks in

	// Forward to 20s past t0 — enough refill time (relative to the rollback
	// baseline) for the bucket to cap at capacity (3). Without the floor,
	// recovery would still be ~600 tokens underwater.
	rl.WithClock(func() time.Time { return t0.Add(20 * time.Second) })
	for i := 0; i < 3; i++ {
		if rec := runRateLimited(rl, "1.2.3.4:1"); rec.Code != http.StatusOK {
			t.Errorf("post-rollback request %d should pass once bucket re-caps; got %d", i+1, rec.Code)
		}
	}
	if rec := runRateLimited(rl, "1.2.3.4:1"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("post-rollback 4th must throttle (bucket drained); got %d", rec.Code)
	}
}

// TestClientIP_EmptyXFFLeftmostFallsThroughToRemoteAddr — closes the
// shared-bucket DoS where `X-Forwarded-For: , 10.0.0.1` keys every request
// on the empty string.
func TestClientIP_EmptyXFFLeftmostFallsThroughToRemoteAddr(t *testing.T) {
	cases := []struct {
		xff      string
		remote   string
		wantHost string
	}{
		{xff: ", 10.0.0.1", remote: "203.0.113.5:5555", wantHost: "203.0.113.5"},
		{xff: "  , 10.0.0.1", remote: "203.0.113.5:5555", wantHost: "203.0.113.5"},
		{xff: "   ", remote: "203.0.113.5:5555", wantHost: "203.0.113.5"},
		{xff: "203.0.113.42, 10.0.0.1", remote: "203.0.113.5:5555", wantHost: "203.0.113.42"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		got := ClientIP(r)
		if got != c.wantHost {
			t.Errorf("ClientIP(xff=%q,remote=%q): want %q, got %q",
				c.xff, c.remote, c.wantHost, got)
		}
	}
}

// TestClientIP_RemoteAddrPortStripped — RemoteAddr like "1.2.3.4:54321" must
// produce bucket key "1.2.3.4" so NAT'd ephemeral ports don't fragment buckets.
func TestClientIP_RemoteAddrPortStripped(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "1.2.3.4:54321"
	if got := ClientIP(r); got != "1.2.3.4" {
		t.Errorf("port not stripped: got %q", got)
	}
}

func TestRateLimiter_XFFLeftmostIsBucketKey(t *testing.T) {
	rl, _ := newTestRateLimiter(t)
	// Build a request with XFF; behind-the-proxy IP should be the bucket key.
	makeReq := func(xff string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/login/github", nil)
		r.Header.Set("X-Forwarded-For", xff)
		r.RemoteAddr = "10.0.0.1:1" // proxy IP — must be ignored
		return r
	}
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, makeReq("203.0.113.5, 10.0.0.1"))
		if w.Code != http.StatusOK {
			t.Fatalf("expected ok, got %d", w.Code)
		}
	}
	// Fourth from the same client IP — even though RemoteAddr differs — throttled.
	w := httptest.NewRecorder()
	rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(w, makeReq("203.0.113.5, 10.0.0.1"))
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("XFF-derived IP must persist across same-XFF requests; got %d", w.Code)
	}
}
