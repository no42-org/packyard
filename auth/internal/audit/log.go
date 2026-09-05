/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

// Package audit defines the operator audit log interface used by admin
// handlers. The persistent SQLite-backed implementation lands in section 6 of
// change 2026-05-21-admin-ui-account-lifecycle; until then handlers wire to
// NoopAuditor so the write call sites can be in place without a storage layer.
package audit

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/no42-org/packyard-auth/internal/logsafe"

	"github.com/no42-org/packyard-auth/internal/auth"
)

// Entry is one row of the audit_log table. ID and Ts are DB-stamped and
// MUST NOT be set by writers — the SQLite layer ignores both at insert time
// (id is AUTOINCREMENT; ts uses the schema's strftime default). They are
// populated by readers (scanAuditRow → ListAuditEntries → API response) so
// the UI can show event time and provide stable per-row references.
type Entry struct {
	ID         int64          `json:"id,omitempty"`
	Ts         time.Time      `json:"ts,omitempty"`
	OperatorID string         `json:"operator_id,omitempty"`
	Action     string         `json:"action"`
	TargetType string         `json:"target_type,omitempty"`
	TargetID   string         `json:"target_id,omitempty"`
	Details    map[string]any `json:"details,omitempty"`
	IP         string         `json:"ip,omitempty"`
	UserAgent  string         `json:"user_agent,omitempty"`
}

// Auditor accepts a single audit entry. Implementations are expected to be
// best-effort fire-and-forget from the handler's point of view: a failure to
// persist an audit row MUST NOT block the operator action that triggered it.
// The interface returns no error for this reason — implementations log
// internally.
type Auditor interface {
	Write(ctx context.Context, e Entry)
}

// WriteFromRequest is a small convenience that copies the ip and user-agent
// off an *http.Request into the Entry before delegating to the Auditor. It
// also auto-fills OperatorID from the request context (via
// auth.OperatorFromContext) when the caller didn't set one explicitly.
//
// An empty OperatorID is legitimate for pre-authentication audit rows
// (login.failure with no operator yet, rate-limit denials). Warn only when
// the auditor is the dev NoopAuditor so it surfaces in local dev without
// polluting production logs.
//
// Defensive: a nil ctx is normalised to context.Background() so the
// downstream Value lookup can't nil-deref. Whitespace-only OperatorID is
// trimmed (counts as empty for the auto-fill check) so a caller passing
// `" "` doesn't end up with a whitespace operator_id in the audit log.
func WriteFromRequest(ctx context.Context, a Auditor, r *http.Request, e Entry) {
	if ctx == nil {
		ctx = context.Background()
	}
	if a == nil {
		// Defensive: a nil Auditor would panic on a.Write. Treat as no-op.
		a = NoopAuditor{}
	}
	if r != nil {
		if e.IP == "" {
			e.IP = clientIP(r)
		}
		if e.UserAgent == "" {
			e.UserAgent = r.UserAgent()
		}
	}
	e.OperatorID = strings.TrimSpace(e.OperatorID)
	if e.OperatorID == "" {
		if op, ok := auth.OperatorFromContext(ctx); ok && op.ID != "" {
			e.OperatorID = op.ID
		}
	}
	if e.OperatorID == "" {
		// Empty OperatorID is legitimate for some pre-authentication
		// audit rows (login.failure, auth.rate_limited). For everything
		// else it's a caller bug or a context-injection regression worth
		// surfacing in any environment, not just dev. The auditor can
		// optionally implement EmptyOperatorWarner to receive the warning
		// in production; the NoopAuditor wires it via its Logger field.
		if w, ok := a.(EmptyOperatorWarner); ok {
			w.WarnEmptyOperator(e)
		}
	}
	a.Write(ctx, e)
}

// EmptyOperatorWarner is an optional capability the Auditor implementation
// can expose so WriteFromRequest can flag unattributed audit rows. Wired up
// for both NoopAuditor (dev) and SQLiteStore (production) so the warning
// fires regardless of environment.
type EmptyOperatorWarner interface {
	WarnEmptyOperator(e Entry)
}

// NoopAuditor discards entries. Used in tests / dev. Holds a slog.Logger
// so the no-op is observable at debug level and so empty-operator warnings
// have a place to land.
type NoopAuditor struct {
	Logger *slog.Logger
}

// Write logs at debug level and returns. Never persists.
func (n NoopAuditor) Write(_ context.Context, e Entry) {
	if n.Logger == nil {
		return
	}
	n.Logger.Debug("audit (noop)",
		slog.String("operator_id", e.OperatorID),
		slog.String("action", e.Action),
		slog.String("target_type", e.TargetType),
		logsafe.Attr("target_id", e.TargetID),
	)
}

// WarnEmptyOperator implements EmptyOperatorWarner.
func (n NoopAuditor) WarnEmptyOperator(e Entry) {
	if n.Logger == nil {
		return
	}
	n.Logger.Warn("audit entry has empty operator_id",
		slog.String("action", e.Action),
		slog.String("target_type", e.TargetType),
		logsafe.Attr("target_id", e.TargetID),
	)
}

// clientIP returns the request's source IP, preferring X-Forwarded-For when
// present (Traefik sets it on the public admin entrypoint). For multi-hop
// forwarding the leftmost token is the original client per RFC 7239 §5.2,
// so we parse and return only that. Trust assumes Traefik is the only proxy
// allowed to write XFF — section 4 will document/enforce that assumption.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	return r.RemoteAddr
}
