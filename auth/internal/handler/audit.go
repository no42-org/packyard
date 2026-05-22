package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/no42-org/packyard-auth/internal/audit"
	"github.com/no42-org/packyard-auth/internal/auth"
	"github.com/no42-org/packyard-auth/internal/store"
)

// AuditHandler serves GET /api/v1/audit (§ 6.3). No mutation surface — only
// the read endpoint exists, so the spec's "audit rows SHALL NOT be modifiable
// or deletable via any API surface" constraint (§ 6.4) is enforced by absence
// of code rather than by a check.
//
// Both `admin` and `readonly` operators may read; the role middleware allows
// GETs for any session, which matches D9.
type AuditHandler struct {
	Audit  store.AuditStore
	Logger *slog.Logger
}

func NewAuditHandler(a store.AuditStore, logger *slog.Logger) *AuditHandler {
	return &AuditHandler{Audit: a, Logger: logger}
}

// List handles GET /api/v1/audit with the spec-mandated filters and D23
// pagination. Time bounds use `[Since, Until)` (Since inclusive, Until
// exclusive); RFC3339-Nano sub-second precision is truncated to seconds to
// match the column's strftime format.
//
// Defense in depth: the route is wired inside the session-protected /api/v1
// group in main.go, but we re-check operator presence here so a future
// routing change can't expose the audit log unauthenticated.
func (h *AuditHandler) List(w http.ResponseWriter, r *http.Request) {
	if _, ok := auth.OperatorFromContext(r.Context()); !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED",
			"no authenticated operator on the request")
		return
	}

	q := r.URL.Query()
	filter := store.AuditFilter{
		OperatorID: q.Get("operator"),
		Action:     q.Get("action"),
		TargetType: q.Get("target_type"),
		TargetID:   q.Get("target_id"),
	}
	if s := q.Get("since"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST",
				"since must be RFC3339 timestamp")
			return
		}
		// Truncate to second precision: schema stores ts at second precision,
		// and `2026-01-01T00:00:00.5Z` would otherwise silently filter at
		// `00:00:00` after string formatting.
		t = t.Truncate(time.Second)
		filter.Since = &t
	}
	if s := q.Get("until"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST",
				"until must be RFC3339 timestamp")
			return
		}
		t = t.Truncate(time.Second)
		filter.Until = &t
	}
	// Mistyped windows (since after until) silently return [] without
	// validation; surface the bug at the API edge instead.
	if filter.Since != nil && filter.Until != nil && filter.Since.After(*filter.Until) {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST",
			"since must be <= until")
		return
	}

	offset, limit, ok := parsePagination(w, r)
	if !ok {
		return
	}

	// Fetch limit+1 to detect more pages without a count query.
	entries, err := h.Audit.ListAuditEntries(r.Context(), filter, offset, limit+1)
	if err != nil {
		h.Logger.Error("list audit entries failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "AUDIT_LIST_FAILED", "failed to list audit entries")
		return
	}
	hasMore := len(entries) > limit
	if hasMore {
		entries = entries[:limit]
	}

	writePaginationLinks(w, r, hasMore, offset, limit)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(auditResponse(entries)); err != nil {
		// Header is already written; can't change status. Log so the
		// truncated body isn't invisible to ops.
		h.Logger.Error("encode audit list failed", slog.String("error", err.Error()))
	}
}

// auditResponse converts []audit.Entry into a stable JSON shape. `id` and
// `ts` are essential for the UI viewer (task 8.8) to render an ordered
// timeline with stable per-row references.
type auditResponseRow struct {
	ID         int64          `json:"id"`
	Ts         time.Time      `json:"ts"`
	OperatorID string         `json:"operator_id,omitempty"`
	Action     string         `json:"action"`
	TargetType string         `json:"target_type,omitempty"`
	TargetID   string         `json:"target_id,omitempty"`
	Details    map[string]any `json:"details,omitempty"`
	IP         string         `json:"ip,omitempty"`
	UserAgent  string         `json:"user_agent,omitempty"`
}

func auditResponse(in []audit.Entry) []auditResponseRow {
	out := make([]auditResponseRow, len(in))
	for i, e := range in {
		out[i] = auditResponseRow{
			ID:         e.ID,
			Ts:         e.Ts,
			OperatorID: e.OperatorID,
			Action:     e.Action,
			TargetType: e.TargetType,
			TargetID:   e.TargetID,
			Details:    e.Details,
			IP:         e.IP,
			UserAgent:  e.UserAgent,
		}
	}
	return out
}
