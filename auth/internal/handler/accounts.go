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
	"net/url"
	"strconv"

	"github.com/no42-org/packyard-auth/internal/logsafe"

	"github.com/go-chi/chi/v5"
	"github.com/no42-org/packyard-auth/internal/audit"
	"github.com/no42-org/packyard-auth/internal/auth"
	"github.com/no42-org/packyard-auth/internal/middleware"
	"github.com/no42-org/packyard-auth/internal/store"
)

// AccountsHandler serves /api/v1/accounts and /api/v1/accounts/{id}/keys.
// Role enforcement happens here (until session middleware lands in § 4); the
// operator stub returned by auth.OperatorFromContext is admin by default so
// the existing dev surface keeps working.
type AccountsHandler struct {
	Accounts            store.AccountStore
	Components          store.ComponentStore
	Auditor             audit.Auditor
	Logger              *slog.Logger
	ComponentVisibility map[string]string
}

// NewAccountsHandler returns a handler with empty maps coerced to non-nil and
// a NoopAuditor when no auditor is supplied (development-mode default).
func NewAccountsHandler(
	accounts store.AccountStore,
	components store.ComponentStore,
	auditor audit.Auditor,
	logger *slog.Logger,
	componentVisibility map[string]string,
) *AccountsHandler {
	if auditor == nil {
		auditor = audit.NoopAuditor{Logger: logger}
	}
	if componentVisibility == nil {
		componentVisibility = map[string]string{}
	}
	return &AccountsHandler{
		Accounts:            accounts,
		Components:          components,
		Auditor:             auditor,
		Logger:              logger,
		ComponentVisibility: componentVisibility,
	}
}

// ─── Request / response shapes ──────────────────────────────────────────────

type createAccountRequest struct {
	Email   string `json:"email"`
	OrgName string `json:"org_name"`
}

type updateAccountRequest struct {
	Email   *string `json:"email,omitempty"`
	OrgName *string `json:"org_name,omitempty"`
	Status  *string `json:"status,omitempty"`
}

type deleteImpactPreview struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Impact  struct {
		KeysRevoked int64 `json:"keys_revoked"`
	} `json:"impact"`
}

type accountDeleteResult struct {
	KeysRevoked int64 `json:"keys_revoked"`
}

// ─── Pagination ─────────────────────────────────────────────────────────────

const (
	defaultPageLimit = 50
	maxPageLimit     = 500
)

// parsePagination reads ?offset= and ?limit= and applies D23 defaults/caps.
//   - missing/empty       → offset 0, limit defaultPageLimit (50)
//   - limit > 500         → 400 LIMIT_TOO_LARGE
//   - negative or non-int → 400 INVALID_REQUEST (used to silently coerce —
//     masked client bugs)
func parsePagination(w http.ResponseWriter, r *http.Request) (offset, limit int, ok bool) {
	offset = 0
	limit = defaultPageLimit

	if v := r.URL.Query().Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST",
				"offset must be a non-negative integer")
			return 0, 0, false
		}
		offset = n
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST",
				"limit must be a positive integer")
			return 0, 0, false
		}
		if n > maxPageLimit {
			writeError(w, http.StatusBadRequest, "LIMIT_TOO_LARGE",
				fmt.Sprintf("limit %d exceeds maximum %d", n, maxPageLimit))
			return 0, 0, false
		}
		limit = n
	}
	return offset, limit, true
}

// writePaginationLinks emits a Link header with rel="next" and rel="prev".
// hasMore is the caller's signal (typically: fetched limit+1 rows, returning
// limit; "more" is whether the extra row was present). prev is included when
// offset > 0. Non-pagination query params are preserved across the link so a
// `?status=suspended` filter survives `next`/`prev` navigation.
func writePaginationLinks(w http.ResponseWriter, r *http.Request, hasMore bool, offset, limit int) {
	base := r.URL.Path
	preserved := preserveQueryExceptPaging(r.URL.Query())

	build := func(off int) string {
		q := url.Values{}
		for k, v := range preserved {
			q[k] = v
		}
		q.Set("offset", strconv.Itoa(off))
		q.Set("limit", strconv.Itoa(limit))
		return fmt.Sprintf(`<%s?%s>`, base, q.Encode())
	}

	var links []string
	if hasMore {
		links = append(links, build(offset+limit)+`; rel="next"`)
	}
	if offset > 0 {
		prev := offset - limit
		if prev < 0 {
			prev = 0
		}
		links = append(links, build(prev)+`; rel="prev"`)
	}
	if len(links) > 0 {
		w.Header().Set("Link", joinLinks(links))
	}
}

// preserveQueryExceptPaging copies the inbound query string minus offset/limit
// so paging links carry forward filters (e.g. ?status=suspended).
func preserveQueryExceptPaging(in url.Values) url.Values {
	out := url.Values{}
	for k, vs := range in {
		if k == "offset" || k == "limit" {
			continue
		}
		out[k] = vs
	}
	return out
}

func joinLinks(links []string) string {
	out := links[0]
	for _, l := range links[1:] {
		out += ", " + l
	}
	return out
}

// ─── Role gate ──────────────────────────────────────────────────────────────

// requireAdmin returns the operator and true when the request operator is
// admin. Otherwise it writes 403 ROLE_DENIED (or 401 UNAUTHORIZED if no
// operator has been injected) and returns false.
func (h *AccountsHandler) requireAdmin(w http.ResponseWriter, r *http.Request) (auth.Operator, bool) {
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

// ─── Handlers ───────────────────────────────────────────────────────────────

// Create handles POST /api/v1/accounts.
func (h *AccountsHandler) Create(w http.ResponseWriter, r *http.Request) {
	op, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req createAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body must be valid JSON")
		return
	}

	account, err := h.Accounts.CreateAccount(r.Context(), store.AccountInput{
		Email:   req.Email,
		OrgName: req.OrgName,
	}, op.ID)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrAccountEmailExists):
			writeError(w, http.StatusConflict, "ACCOUNT_EMAIL_EXISTS",
				fmt.Sprintf("an account with email %q already exists", req.Email))
			return
		case errors.Is(err, store.ErrAccountInvalid):
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST",
				"account email is required and must be a valid canonical address")
			return
		default:
			h.Logger.Error("create account failed", slog.String("error", err.Error()))
			writeError(w, http.StatusInternalServerError, "ACCOUNT_CREATE_FAILED",
				"failed to create account")
			return
		}
	}

	audit.WriteFromRequest(r.Context(), h.Auditor, r, audit.Entry{
		OperatorID: op.ID,
		Action:     "account.create",
		TargetType: "account",
		TargetID:   account.ID,
		Details: map[string]any{
			"email": account.Email,
			// org_name is operator-controlled and untrimmed; bound it.
			"org_name": middleware.TruncateAuditField(account.OrgName),
		},
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(account)
}

// List handles GET /api/v1/accounts.
func (h *AccountsHandler) List(w http.ResponseWriter, r *http.Request) {
	statusFilter := store.AccountStatus(r.URL.Query().Get("status"))
	if statusFilter != "" &&
		statusFilter != store.AccountStatusActive &&
		statusFilter != store.AccountStatusSuspended &&
		statusFilter != store.AccountStatusDeleted {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST",
			fmt.Sprintf("status filter %q is not one of active, suspended, deleted", statusFilter))
		return
	}

	offset, limit, ok := parsePagination(w, r)
	if !ok {
		return
	}

	// Fetch one row beyond the page so we know whether to emit rel="next"
	// without a separate COUNT query.
	accounts, err := h.Accounts.ListAccounts(r.Context(), statusFilter, offset, limit+1)
	if err != nil {
		h.Logger.Error("list accounts failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "ACCOUNT_LIST_FAILED", "failed to list accounts")
		return
	}
	hasMore := len(accounts) > limit
	if hasMore {
		accounts = accounts[:limit]
	}

	writePaginationLinks(w, r, hasMore, offset, limit)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(accounts)
}

// Get handles GET /api/v1/accounts/{id}.
func (h *AccountsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	account, err := h.Accounts.GetAccount(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrAccountNotFound) {
			writeError(w, http.StatusNotFound, "ACCOUNT_NOT_FOUND",
				fmt.Sprintf("account %q not found", id))
			return
		}
		h.Logger.Error("get account failed", logsafe.Attr("id", id), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "ACCOUNT_GET_FAILED", "failed to get account")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(account)
}

// Update handles PATCH /api/v1/accounts/{id}.
// Status transitions are limited to active↔suspended; "deleted" must go
// through the DELETE endpoint.
func (h *AccountsHandler) Update(w http.ResponseWriter, r *http.Request) {
	op, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}

	id := chi.URLParam(r, "id")
	// Pre-flight: deleted accounts aren't patchable.
	existing, err := h.Accounts.GetAccount(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrAccountNotFound) {
			writeError(w, http.StatusNotFound, "ACCOUNT_NOT_FOUND",
				fmt.Sprintf("account %q not found", id))
			return
		}
		h.Logger.Error("get account for update failed", logsafe.Attr("id", id), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "ACCOUNT_GET_FAILED", "failed to get account")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req updateAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body must be valid JSON")
		return
	}

	upd := store.AccountUpdate{
		Email:   req.Email,
		OrgName: req.OrgName,
	}

	action := "account.update"
	if req.Status != nil {
		target := store.AccountStatus(*req.Status)
		if target == store.AccountStatusDeleted || existing.Status == store.AccountStatusDeleted {
			writeError(w, http.StatusBadRequest, "INVALID_STATUS_TRANSITION",
				"the deleted status is only set via DELETE /api/v1/accounts/{id}?confirm={id}")
			return
		}
		if target != store.AccountStatusActive && target != store.AccountStatusSuspended {
			writeError(w, http.StatusBadRequest, "INVALID_STATUS_TRANSITION",
				fmt.Sprintf("status %q is not a valid transition target", target))
			return
		}
		upd.Status = &target

		switch {
		case existing.Status == store.AccountStatusActive && target == store.AccountStatusSuspended:
			action = "account.suspend"
		case existing.Status == store.AccountStatusSuspended && target == store.AccountStatusActive:
			action = "account.reactivate"
		}
	}

	updated, err := h.Accounts.UpdateAccount(r.Context(), id, upd)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrAccountNotFound):
			writeError(w, http.StatusNotFound, "ACCOUNT_NOT_FOUND",
				fmt.Sprintf("account %q not found", id))
			return
		case errors.Is(err, store.ErrAccountEmailExists):
			writeError(w, http.StatusConflict, "ACCOUNT_EMAIL_EXISTS",
				"the requested email is already used by another account")
			return
		case errors.Is(err, store.ErrAccountInvalid):
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST",
				"account email is required and must be a valid canonical address")
			return
		case errors.Is(err, store.ErrInvalidStatusTransition):
			writeError(w, http.StatusBadRequest, "INVALID_STATUS_TRANSITION",
				"the deleted status is only set via DELETE /api/v1/accounts/{id}?confirm={id}")
			return
		case errors.Is(err, store.ErrLegacyAccountProtected):
			writeError(w, http.StatusForbidden, "ACCOUNT_RESERVED",
				"the legacy account cannot be suspended or deleted")
			return
		default:
			h.Logger.Error("update account failed", logsafe.Attr("id", id), slog.String("error", err.Error()))
			writeError(w, http.StatusInternalServerError, "ACCOUNT_UPDATE_FAILED", "failed to update account")
			return
		}
	}

	audit.WriteFromRequest(r.Context(), h.Auditor, r, audit.Entry{
		OperatorID: op.ID,
		Action:     action,
		TargetType: "account",
		TargetID:   updated.ID,
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(updated)
}

// Delete handles DELETE /api/v1/accounts/{id}.
// Without ?confirm={id} → 409 CONFIRM_REQUIRED with an impact preview.
// With ?confirm={id} matching the URL id → atomic cascade revoke + soft-delete.
func (h *AccountsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	op, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}

	id := chi.URLParam(r, "id")
	confirm := r.URL.Query().Get("confirm")

	// Verify the account exists (and is not already deleted) before either
	// branch — both 409 confirm-required and the cascade need a real account.
	if _, err := h.Accounts.GetAccount(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrAccountNotFound) {
			writeError(w, http.StatusNotFound, "ACCOUNT_NOT_FOUND",
				fmt.Sprintf("account %q not found", id))
			return
		}
		h.Logger.Error("get account for delete failed", logsafe.Attr("id", id), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "ACCOUNT_GET_FAILED", "failed to get account")
		return
	}

	if confirm != id {
		n, err := h.Accounts.CountActiveAccountKeys(r.Context(), id)
		if err != nil {
			h.Logger.Error("count account keys for impact preview failed",
				logsafe.Attr("id", id), slog.String("error", err.Error()))
			writeError(w, http.StatusInternalServerError, "ACCOUNT_DELETE_FAILED",
				"failed to compute delete impact preview")
			return
		}
		preview := deleteImpactPreview{
			Code:    "CONFIRM_REQUIRED",
			Message: fmt.Sprintf("repeat the request with ?confirm=%s to proceed", id),
		}
		preview.Impact.KeysRevoked = n
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(preview)
		return
	}

	revoked, err := h.Accounts.DeleteAccountWithCascade(r.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrAccountNotFound):
			writeError(w, http.StatusNotFound, "ACCOUNT_NOT_FOUND",
				fmt.Sprintf("account %q not found", id))
			return
		case errors.Is(err, store.ErrLegacyAccountProtected):
			writeError(w, http.StatusForbidden, "ACCOUNT_RESERVED",
				"the legacy account cannot be deleted")
			return
		default:
			h.Logger.Error("delete account failed", logsafe.Attr("id", id), slog.String("error", err.Error()))
			writeError(w, http.StatusInternalServerError, "ACCOUNT_DELETE_FAILED", "failed to delete account")
			return
		}
	}

	audit.WriteFromRequest(r.Context(), h.Auditor, r, audit.Entry{
		OperatorID: op.ID,
		Action:     "account.delete",
		TargetType: "account",
		TargetID:   id,
		Details:    map[string]any{"keys_revoked": revoked},
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(accountDeleteResult{KeysRevoked: revoked})
}

// ListKeys handles GET /api/v1/accounts/{id}/keys.
func (h *AccountsHandler) ListKeys(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := h.Accounts.GetAccount(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrAccountNotFound) {
			writeError(w, http.StatusNotFound, "ACCOUNT_NOT_FOUND",
				fmt.Sprintf("account %q not found", id))
			return
		}
		h.Logger.Error("get account for key list failed", logsafe.Attr("id", id), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "ACCOUNT_GET_FAILED", "failed to get account")
		return
	}

	offset, limit, ok := parsePagination(w, r)
	if !ok {
		return
	}

	keys, err := h.Accounts.ListAccountKeys(r.Context(), id, offset, limit+1)
	if err != nil {
		h.Logger.Error("list account keys failed", logsafe.Attr("id", id), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "ACCOUNT_KEYS_LIST_FAILED", "failed to list account keys")
		return
	}
	hasMore := len(keys) > limit
	if hasMore {
		keys = keys[:limit]
	}

	wrapped := make([]*keyResponse, len(keys))
	for i, k := range keys {
		wrapped[i] = h.wrapKey(k)
	}

	writePaginationLinks(w, r, hasMore, offset, limit)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(wrapped)
}

// IssueKey handles POST /api/v1/accounts/{id}/keys.
func (h *AccountsHandler) IssueKey(w http.ResponseWriter, r *http.Request) {
	op, ok := h.requireAdmin(w, r)
	if !ok {
		return
	}

	id := chi.URLParam(r, "id")
	// GetAccount returns ErrAccountNotFound for deleted accounts, satisfying
	// the spec "cannot issue key for deleted account → 404 ACCOUNT_NOT_FOUND".
	account, err := h.Accounts.GetAccount(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrAccountNotFound) {
			writeError(w, http.StatusNotFound, "ACCOUNT_NOT_FOUND",
				fmt.Sprintf("account %q not found", id))
			return
		}
		h.Logger.Error("get account for issue key failed", logsafe.Attr("id", id), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "ACCOUNT_GET_FAILED", "failed to get account")
		return
	}
	if account.Status != store.AccountStatusActive {
		writeError(w, http.StatusConflict, "ACCOUNT_SUSPENDED",
			fmt.Sprintf("account %q is %s; reactivate before issuing keys", id, account.Status))
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req createKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body must be valid JSON")
		return
	}

	// The URL path's {id} is authoritative; reject a body that contradicts it
	// rather than silently using one and discarding the other.
	if req.AccountID != "" && req.AccountID != id {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST",
			"body account_id must match the URL account id (or be omitted)")
		return
	}

	if _, err := h.Components.GetComponent(r.Context(), req.Component); err != nil {
		if errors.Is(err, store.ErrComponentNotFound) {
			writeError(w, http.StatusBadRequest, "INVALID_COMPONENT",
				fmt.Sprintf("component %q not found", req.Component))
			return
		}
		h.Logger.Error("validate component failed",
			logsafe.Attr("component", req.Component), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "KEY_CREATE_FAILED", "failed to validate component")
		return
	}

	key, err := h.Accounts.CreateKeyForAccount(r.Context(), id, req.Component, req.Label, req.ExpiresAt)
	if err != nil {
		// ErrAccountNotFound can come from FK enforcement if the account is
		// removed between GetAccount and the insert (extremely narrow race).
		if errors.Is(err, store.ErrAccountNotFound) {
			writeError(w, http.StatusNotFound, "ACCOUNT_NOT_FOUND",
				fmt.Sprintf("account %q not found", id))
			return
		}
		h.Logger.Error("create key for account failed", logsafe.Attr("id", id), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "KEY_CREATE_FAILED", "failed to create key")
		return
	}

	audit.WriteFromRequest(r.Context(), h.Auditor, r, audit.Entry{
		OperatorID: op.ID,
		Action:     "key.issue",
		TargetType: "key",
		TargetID:   key.ID,
		Details: map[string]any{
			"account_id": id,
			"component":  req.Component,
			"label":      middleware.TruncateAuditField(req.Label),
		},
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(h.wrapKey(key))
}

// wrapKey mirrors KeysHandler.wrapKey for the accounts-keys endpoints; we
// can't reach the other handler's instance here, so duplicate the visibility
// fall-through.
func (h *AccountsHandler) wrapKey(k *store.Key) *keyResponse {
	vis := h.ComponentVisibility[k.Component]
	if vis == "" {
		vis = "private"
	}
	return &keyResponse{Key: k, ComponentVisibility: vis}
}
