package middleware

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/no42-org/packyard-auth/internal/audit"
)

// Rate-limit defaults per D21 of change 2026-05-21-admin-ui-account-lifecycle:
// capacity 10, refill 1 token per 6 seconds → 10 requests/minute sustained.
const (
	DefaultRateLimitCapacity   = 10
	DefaultRateLimitRefillPerS = 1.0 / 6.0
	// auditCoalesceWindow bounds audit pressure under attack — we only emit
	// one `auth.rate_limited` row per (IP, window). The window matches the
	// time to fully refill an empty bucket so an attacker can't squeeze
	// more than one audit row per "throttle epoch" no matter the RPS.
	auditCoalesceWindow = time.Minute
	// auditPathMaxLen truncates attacker-controllable paths in audit rows.
	auditPathMaxLen = 256
)

// bucket tracks the per-IP token state. tokens is a float so partial refills
// accumulate correctly across requests. lastAuditAt coalesces audit-row
// emission so a sustained attack doesn't flood the audit log.
type bucket struct {
	tokens      float64
	lastUpdate  time.Time
	lastAuditAt time.Time
}

// RateLimiter is a token-bucket rate limiter keyed by source IP, suitable
// for the OAuth login/callback endpoints (D21). It evicts idle buckets via
// a background reaper so the map does not grow unbounded under failed-flow
// churn or scan attacks.
type RateLimiter struct {
	mu               sync.Mutex
	buckets          map[string]*bucket
	capacity         float64
	refillPerSecond  float64
	now              func() time.Time
	auditor          audit.Auditor
	logger           *slog.Logger
}

// RateLimiterConfig wires the limiter to its audit sink and runtime knobs.
type RateLimiterConfig struct {
	Capacity        int     // tokens; default DefaultRateLimitCapacity
	RefillPerSecond float64 // tokens/second; default DefaultRateLimitRefillPerS
	Auditor         audit.Auditor
	Logger          *slog.Logger
}

// NewRateLimiter constructs the limiter and starts a background reaper that
// removes buckets that have been full (untouched) for at least one capacity-
// worth of refill time. Reaper exits when ctx is cancelled.
func NewRateLimiter(ctx context.Context, cfg RateLimiterConfig) *RateLimiter {
	cap := float64(cfg.Capacity)
	if cap <= 0 {
		cap = DefaultRateLimitCapacity
	}
	refill := cfg.RefillPerSecond
	if refill <= 0 {
		refill = DefaultRateLimitRefillPerS
	}
	rl := &RateLimiter{
		buckets:         make(map[string]*bucket),
		capacity:        cap,
		refillPerSecond: refill,
		now:             time.Now,
		auditor:         cfg.Auditor,
		logger:          cfg.Logger,
	}
	go rl.reapLoop(ctx, time.Minute)
	return rl
}

// Middleware enforces the per-IP bucket against the configured capacity.
// Exhaustion returns 429 + RATE_LIMITED + a coalesced `auth.rate_limited`
// audit row (at most one per IP per auditCoalesceWindow).
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := ClientIP(r)
		allowed, shouldAudit := rl.check(ip)
		if allowed {
			next.ServeHTTP(w, r)
			return
		}
		if shouldAudit && rl.auditor != nil {
			audit.WriteFromRequest(r.Context(), rl.auditor, r, audit.Entry{
				Action:     "auth.rate_limited",
				TargetType: "ip",
				TargetID:   ip,
				Details: map[string]any{
					"ip":   ip,
					"path": truncate(r.URL.Path, auditPathMaxLen),
				},
			})
		}
		writeError(w, http.StatusTooManyRequests, "RATE_LIMITED",
			"too many requests from this source; slow down")
	})
}

// check consumes one token from the bucket for ip and decides whether the
// denial (if any) should produce an audit row. Returns:
//
//	allowed     — the request may proceed
//	shouldAudit — caller should write an audit row (false when allowed, or
//	              when this bucket has produced an audit row within
//	              auditCoalesceWindow)
func (rl *RateLimiter) check(ip string) (allowed, shouldAudit bool) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.now()
	b, ok := rl.buckets[ip]
	if !ok {
		// New IP: full bucket, immediately consume one.
		rl.buckets[ip] = &bucket{tokens: rl.capacity - 1, lastUpdate: now}
		return true, false
	}

	// Refill based on time elapsed since last update, capped at capacity.
	// Floor at zero so a backward clock step (NTP rollback) can't drive the
	// bucket arbitrarily negative — otherwise recovery would take
	// (abs(tokens)+1) × 6s of forward time, blackholing the IP for hours.
	elapsed := now.Sub(b.lastUpdate).Seconds()
	b.tokens += elapsed * rl.refillPerSecond
	if b.tokens > rl.capacity {
		b.tokens = rl.capacity
	}
	if b.tokens < 0 {
		b.tokens = 0
	}
	b.lastUpdate = now

	if b.tokens < 1 {
		// Throttled. Audit only the first denial in each coalesce window
		// so a sustained attack can't flood the audit log.
		if now.Sub(b.lastAuditAt) >= auditCoalesceWindow {
			b.lastAuditAt = now
			return false, true
		}
		return false, false
	}
	b.tokens--
	return true, false
}

// truncate caps s at maxLen runes (not bytes) to avoid cutting a multi-byte
// rune in half; appends an ellipsis marker when truncation occurred.
// Exported (lowercase) within the package; handlers that emit audit details
// for attacker-controllable strings call TruncateAuditField directly.
func truncate(s string, maxLen int) string {
	if maxLen <= 0 || len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

// AuditFieldMaxLen is the max length any operator-controllable string field
// SHOULD be when emitted into an audit_log.details value. Matches
// auditPathMaxLen above (used by the rate-limiter for the same reason).
const AuditFieldMaxLen = 256

// TruncateAuditField bounds operator-controllable strings (account label,
// org_name, role-denial paths) before they land in audit_log.details. Bounds
// audit-storage cost under attacker pressure.
func TruncateAuditField(s string) string {
	return truncate(s, AuditFieldMaxLen)
}

func (rl *RateLimiter) reapLoop(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			rl.reap()
		case <-ctx.Done():
			return
		}
	}
}

// reap removes buckets that are full (no requests in long enough to refill
// completely). Keeps the map bounded under scan attacks without affecting
// correctness — recreating a full bucket on the next request is harmless.
func (rl *RateLimiter) reap() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := rl.now()
	for ip, b := range rl.buckets {
		elapsed := now.Sub(b.lastUpdate).Seconds()
		refilled := b.tokens + elapsed*rl.refillPerSecond
		if refilled >= rl.capacity {
			delete(rl.buckets, ip)
		}
	}
}

// WithClock swaps the time source for tests. Safe to call from a test
// goroutine concurrent with request flow because the assignment is mutex-
// guarded; check() and reap() read s.now under the same lock.
func (rl *RateLimiter) WithClock(now func() time.Time) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.now = now
}

// ClientIP returns the source IP for r, preferring the leftmost X-Forwarded-For
// token (RFC 7239 §5.2) when present. If the leftmost token is empty or
// whitespace (`X-Forwarded-For: , 10.0.0.1` — attacker-controllable), the
// header is ignored and the function falls through to RemoteAddr. The port
// is stripped from RemoteAddr so a single client behind NAT doesn't get a
// fresh rate-limit bucket per TCP connection.
//
// This is the shared helper for the middleware layer; audit/log.go has a
// near-identical copy to avoid an import cycle. Keep the two in sync.
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		token := xff
		if i := strings.IndexByte(xff, ','); i >= 0 {
			token = xff[:i]
		}
		token = strings.TrimSpace(token)
		if token != "" {
			return token
		}
		// Empty/whitespace leftmost: header was malformed or
		// attacker-crafted; fall through to RemoteAddr.
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
