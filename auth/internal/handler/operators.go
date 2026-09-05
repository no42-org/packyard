/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/no42-org/packyard-auth/internal/logsafe"

	"github.com/go-chi/chi/v5"

	"github.com/no42-org/packyard-auth/internal/audit"
	"github.com/no42-org/packyard-auth/internal/auth"
	"github.com/no42-org/packyard-auth/internal/store"
)

// OperatorsHandler serves /api/v1/operators (§ 5.2). Admin-only across the
// board — readonly operators cannot list, add, or modify operator records
// even though they can read other GET endpoints. The role gate is enforced
// at the handler level via requireAdmin (mirrors AccountsHandler).
//
// Disabling or demoting an operator deletes their existing sessions so the
// change takes effect on their next request rather than at session expiry.
type OperatorsHandler struct {
	Operators store.OperatorStore
	Sessions  store.SessionStore // for force-logout on disable / role change
	Auditor   audit.Auditor
	Logger    *slog.Logger
}

func NewOperatorsHandler(ops store.OperatorStore, sessions store.SessionStore, auditor audit.Auditor, logger *slog.Logger) *OperatorsHandler {
	if auditor == nil {
		auditor = audit.NoopAuditor{Logger: logger}
	}
	return &OperatorsHandler{
		Operators: ops,
		Sessions:  sessions,
		Auditor:   auditor,
		Logger:    logger,
	}
}

// ─── Request / response shapes ──────────────────────────────────────────────

type createOperatorRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"` // "admin" or "readonly"; defaults to "admin"
}

type updateOperatorRequest struct {
	Role   *string `json:"role,omitempty"`
	Status *string `json:"status,omitempty"`
}

// ─── Role gate (admin-only across all operator routes) ──────────────────────

func (h *OperatorsHandler) requireAdmin(w http.ResponseWriter, r *http.Request) (auth.Operator, bool) {
	op, ok := auth.OperatorFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED",
			"no authenticated operator on the request")
		return op, false
	}
	if !op.IsAdmin() {
		writeError(w, http.StatusForbidden, "ROLE_DENIED",
			"this endpoint requires the admin role")
		return op, false
	}
	return op, true
}

// ─── List ───────────────────────────────────────────────────────────────────

// List handles GET /api/v1/operators. Admin-only per spec § 5.2.
func (h *OperatorsHandler) List(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(w, r); !ok {
		return
	}
	offset, limit, ok := parsePagination(w, r)
	if !ok {
		return
	}
	ops, err := h.Operators.ListOperators(r.Context(), offset, limit+1)
	if err != nil {
		h.Logger.Error("list operators failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "OPERATOR_LIST_FAILED",
			"failed to list operators")
		return
	}
	hasMore := len(ops) > limit
	if hasMore {
		ops = ops[:limit]
	}
	writePaginationLinks(w, r, hasMore, offset, limit)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(ops); err != nil {
		h.Logger.Error("encode operators list failed", slog.String("error", err.Error()))
	}
}

// ─── Create (Allowlist) ─────────────────────────────────────────────────────

// Create handles POST /api/v1/operators. Admin-only. Email is required and
// canonicalised; role defaults to admin per the bootstrap semantics (the
// first operator added by an existing admin is presumed peer-admin unless
// explicitly set to readonly).
func (h *OperatorsHandler) Create(w http.ResponseWriter, r *http.Request) {
	op, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req createOperatorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body must be valid JSON")
		return
	}
	role := store.OperatorRoleAdmin
	if req.Role != "" {
		switch store.OperatorRole(req.Role) {
		case store.OperatorRoleAdmin, store.OperatorRoleReadonly:
			role = store.OperatorRole(req.Role)
		default:
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST",
				fmt.Sprintf("role %q is not one of admin, readonly", req.Role))
			return
		}
	}
	created, err := h.Operators.AllowlistOperator(r.Context(), req.Email, role, op.ID)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrOperatorExists):
			writeError(w, http.StatusConflict, "OPERATOR_EMAIL_EXISTS",
				fmt.Sprintf("operator with email %q already exists", req.Email))
			return
		case errors.Is(err, store.ErrOperatorInvalid):
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST",
				"operator email is required and must be a valid canonical address")
			return
		default:
			h.Logger.Error("allowlist operator failed", slog.String("error", err.Error()))
			writeError(w, http.StatusInternalServerError, "OPERATOR_CREATE_FAILED",
				"failed to create operator")
			return
		}
	}
	audit.WriteFromRequest(r.Context(), h.Auditor, r, audit.Entry{
		OperatorID: op.ID,
		Action:     "operator.add",
		TargetType: "operator",
		TargetID:   created.ID,
		Details: map[string]any{
			"email": created.Email,
			"role":  string(created.Role),
		},
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(created)
}

// ─── Update (role + status) ─────────────────────────────────────────────────

// Update handles PATCH /api/v1/operators/{id}. Admin-only. Accepts role +
// status; either may be set independently, but at least one is required.
//
// Atomicity: the count guard + role + status updates run inside a single
// serializable transaction in UpdateOperatorAtomically, so two concurrent
// PATCHes cannot both pass the "≥1 active admin" check and commit, and a
// partial failure cannot leave the row in a half-state. The guard is global,
// not actor-scoped — an admin cannot demote/disable the last *other* admin
// either.
//
// Forced-logout: if status transitions active→disabled, or role transitions
// admin→readonly, the target operator's existing sessions are removed so the
// change takes effect on their next request. Failures are logged but the
// PATCH still returns 200 — the DB mutation already committed and the next
// request will hit the session-revalidation path.
func (h *OperatorsHandler) Update(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req updateOperatorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body must be valid JSON")
		return
	}

	var newRole *store.OperatorRole
	var newStatus *store.OperatorStatus
	if req.Role != nil {
		switch store.OperatorRole(*req.Role) {
		case store.OperatorRoleAdmin, store.OperatorRoleReadonly:
			role := store.OperatorRole(*req.Role)
			newRole = &role
		default:
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST",
				fmt.Sprintf("role %q is not one of admin, readonly", *req.Role))
			return
		}
	}
	if req.Status != nil {
		switch store.OperatorStatus(*req.Status) {
		case store.OperatorStatusActive, store.OperatorStatusDisabled:
			status := store.OperatorStatus(*req.Status)
			newStatus = &status
		default:
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST",
				fmt.Sprintf("status %q is not one of active, disabled", *req.Status))
			return
		}
	}
	if newRole == nil && newStatus == nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST",
			"at least one of role, status is required")
		return
	}

	before, after, err := h.Operators.UpdateOperatorAtomically(r.Context(), id, newRole, newStatus)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrOperatorNotFound):
			writeError(w, http.StatusNotFound, "OPERATOR_NOT_FOUND",
				fmt.Sprintf("operator %q not found", id))
		case errors.Is(err, store.ErrOperatorSelfLockout):
			writeError(w, http.StatusForbidden, "OPERATOR_SELF_LOCKOUT",
				"refusing to leave zero active admins; ask another admin to make this change")
		default:
			h.Logger.Error("update operator failed",
				logsafe.Attr("id", id), slog.String("error", err.Error()))
			writeError(w, http.StatusInternalServerError, "OPERATOR_UPDATE_FAILED",
				"failed to update operator")
		}
		return
	}

	if newRole != nil && before.Role != after.Role {
		audit.WriteFromRequest(r.Context(), h.Auditor, r, audit.Entry{
			OperatorID: actor.ID,
			Action:     "operator.role_change",
			TargetType: "operator",
			TargetID:   id,
			Details: map[string]any{
				"from": string(before.Role),
				"to":   string(after.Role),
			},
		})
	}
	if newStatus != nil && before.Status != after.Status {
		action := "operator.disable"
		if after.Status == store.OperatorStatusActive {
			action = "operator.enable"
		}
		audit.WriteFromRequest(r.Context(), h.Auditor, r, audit.Entry{
			OperatorID: actor.ID,
			Action:     action,
			TargetType: "operator",
			TargetID:   id,
			Details: map[string]any{
				"from": string(before.Status),
				"to":   string(after.Status),
			},
		})
	}

	demoted := before.Role == store.OperatorRoleAdmin && after.Role == store.OperatorRoleReadonly
	disabled := before.Status == store.OperatorStatusActive && after.Status == store.OperatorStatusDisabled
	if demoted || disabled {
		if err := h.Sessions.DeleteOperatorSessions(r.Context(), id); err != nil {
			h.Logger.Warn("force-logout failed",
				logsafe.Attr("operator_id", id),
				slog.String("error", err.Error()))
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(after)
}
