// Package handler contains HTTP handlers for the packyard-auth service.
package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/no42-org/packyard-auth/internal/audit"
	"github.com/no42-org/packyard-auth/internal/middleware"
	"github.com/no42-org/packyard-auth/internal/store"
)

// KeysHandler handles admin API key management endpoints (Epic 3).
// Construct with NewKeysHandler to guarantee non-nil component maps.
type KeysHandler struct {
	Store               store.KeyStore
	ComponentStore      store.ComponentStore // DB lookup for Create component validation (D1)
	AccountStore        store.AccountStore   // resolves account existence + status for key creation
	Auditor             audit.Auditor        // writes key.issue / key.revoke rows (§ 6.2)
	Logger              *slog.Logger
	ValidComponents     map[string]bool   // set for O(1) membership checks (List filter only)
	ValidComponentList  string            // pre-formatted for error messages
	ComponentVisibility map[string]string // component name → "public" | "private"
}

// NewKeysHandler returns a KeysHandler with nil maps coerced to empty maps so
// that component lookups in List and Create never misbehave silently. A nil
// auditor is coerced to NoopAuditor so the call sites in Create/Delete are
// safe to invoke unconditionally.
func NewKeysHandler(st store.KeyStore, componentStore store.ComponentStore, accountStore store.AccountStore, auditor audit.Auditor, logger *slog.Logger, validComponents map[string]bool, validComponentList string, componentVisibility map[string]string) *KeysHandler {
	if validComponents == nil {
		validComponents = map[string]bool{}
	}
	if componentVisibility == nil {
		componentVisibility = map[string]string{}
	}
	if auditor == nil {
		auditor = audit.NoopAuditor{Logger: logger}
	}
	return &KeysHandler{
		Store:               st,
		ComponentStore:      componentStore,
		AccountStore:        accountStore,
		Auditor:             auditor,
		Logger:              logger,
		ValidComponents:     validComponents,
		ValidComponentList:  validComponentList,
		ComponentVisibility: componentVisibility,
	}
}

// keyResponse wraps a store.Key with the component's current visibility, computed at
// serialisation time from ComponentVisibility so it is never stored in the database.
type keyResponse struct {
	*store.Key
	ComponentVisibility string `json:"component_visibility"`
}

// wrapKey converts a store.Key into a keyResponse, defaulting visibility to "private"
// when the component is absent from the map (e.g. component removed from config).
func (h *KeysHandler) wrapKey(k *store.Key) *keyResponse {
	vis := h.ComponentVisibility[k.Component]
	if vis == "" {
		vis = "private"
	}
	return &keyResponse{Key: k, ComponentVisibility: vis}
}

// createKeyRequest is the JSON body for POST /api/v1/keys.
type createKeyRequest struct {
	AccountID string     `json:"account_id"`
	Component string     `json:"component"`
	Label     string     `json:"label"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// apiError is the structured error body returned by all admin API error responses (FR20).
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeError writes a JSON error response with the given HTTP status, code, and message.
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiError{Code: code, Message: message})
}

// Get handles GET /api/v1/keys/{id} — returns a single key by ID regardless of active status.
func (h *KeysHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	key, err := h.Store.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "KEY_NOT_FOUND",
				fmt.Sprintf("key %q not found", id))
			return
		}
		h.Logger.Error("failed to get key", slog.String("id", id), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "KEY_GET_FAILED", "failed to get key")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(h.wrapKey(key))
}

// Delete handles DELETE /api/v1/keys/{id} — revokes a key immediately.
// Returns 204 on success or if the key is already revoked (idempotent).
// Returns 404 if the key ID does not exist at all.
//
// Audit semantics: a `key.revoke` row is written only when this call actually
// revoked an active key. Idempotent calls (already revoked) and 404s do not
// emit audit rows — they had no effect.
func (h *KeysHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	err := h.Store.RevokeKey(r.Context(), id)
	if err == nil {
		audit.WriteFromRequest(r.Context(), h.Auditor, r, audit.Entry{
			Action:     "key.revoke",
			TargetType: "key",
			TargetID:   id,
		})
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if errors.Is(err, store.ErrNotFound) {
		// RevokeKey returns ErrNotFound for two cases:
		// (a) key truly doesn't exist, OR (b) key exists but active=0 (already revoked).
		// SQLite RowsAffected=0 when the UPDATE changes nothing — so both map to ErrNotFound.
		// Use GetByID (returns revoked keys without error) to distinguish.
		_, getErr := h.Store.GetByID(r.Context(), id)
		if getErr == nil {
			// Key exists but is already revoked — idempotent 204; no audit.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if errors.Is(getErr, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "KEY_NOT_FOUND",
				fmt.Sprintf("key %q not found", id))
			return
		}
		h.Logger.Error("failed to get key after revoke", slog.String("id", id), slog.String("error", getErr.Error()))
		writeError(w, http.StatusInternalServerError, "KEY_DELETE_FAILED", "failed to get key after revoke")
		return
	}

	h.Logger.Error("failed to revoke key", slog.String("id", id), slog.String("error", err.Error()))
	writeError(w, http.StatusInternalServerError, "KEY_DELETE_FAILED", "failed to revoke key")
}

// List handles GET /api/v1/keys — returns keys, optionally filtered by
// ?component= and/or ?account=, with D23-compliant pagination.
func (h *KeysHandler) List(w http.ResponseWriter, r *http.Request) {
	component := r.URL.Query().Get("component")
	if component != "" && !h.ValidComponents[component] {
		writeError(w, http.StatusBadRequest, "INVALID_COMPONENT",
			fmt.Sprintf("component %q is not valid; must be one of: %s", component, h.ValidComponentList))
		return
	}
	account := r.URL.Query().Get("account")

	offset, limit, ok := parsePagination(w, r)
	if !ok {
		return
	}

	keys, err := h.Store.ListKeys(r.Context(), component, account, offset, limit+1)
	if err != nil {
		h.Logger.Error("failed to list keys", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "KEY_LIST_FAILED", "failed to list keys")
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

// Create handles POST /api/v1/keys — provisions a new component-scoped subscription key.
// Requires account_id (§ 3.1); unknown or deleted accounts return 404 ACCOUNT_NOT_FOUND.
func (h *KeysHandler) Create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB
	var req createKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body must be valid JSON")
		return
	}

	if req.AccountID == "" {
		writeError(w, http.StatusBadRequest, "MISSING_ACCOUNT_ID",
			"the account_id field is required when issuing a key")
		return
	}

	// The synthetic legacy account is the FK target for pre-migration keys
	// only — operators MUST NOT mint new keys under it via the public API
	// (defeats lineage; see change § 1 review decision).
	if req.AccountID == "legacy" {
		writeError(w, http.StatusBadRequest, "ACCOUNT_RESERVED",
			"the legacy account is reserved for migration and cannot be used to issue new keys")
		return
	}

	if h.AccountStore != nil {
		account, err := h.AccountStore.GetAccount(r.Context(), req.AccountID)
		if err != nil {
			if errors.Is(err, store.ErrAccountNotFound) {
				// Spec § keys-api-response-codes distinguishes unknown
				// (400 ACCOUNT_NOT_FOUND) from deleted; since the store
				// hides deleted via GetAccount, every ErrAccountNotFound
				// here means the account is unknown to the API surface.
				writeError(w, http.StatusBadRequest, "ACCOUNT_NOT_FOUND",
					fmt.Sprintf("account %q not found", req.AccountID))
				return
			}
			h.Logger.Error("failed to resolve account",
				slog.String("account_id", req.AccountID), slog.String("error", err.Error()))
			writeError(w, http.StatusInternalServerError, "KEY_CREATE_FAILED", "failed to resolve account")
			return
		}
		// D11: don't mint keys under a suspended account — forward-auth would
		// deny them immediately, leaving a dead key in the store.
		if account.Status != store.AccountStatusActive {
			writeError(w, http.StatusConflict, "ACCOUNT_SUSPENDED",
				fmt.Sprintf("account %q is %s; reactivate before issuing keys", req.AccountID, account.Status))
			return
		}
	}

	if _, err := h.ComponentStore.GetComponent(r.Context(), req.Component); err != nil {
		if errors.Is(err, store.ErrComponentNotFound) {
			writeError(w, http.StatusBadRequest, "INVALID_COMPONENT",
				fmt.Sprintf("component %q not found", req.Component))
			return
		}
		h.Logger.Error("failed to validate component", slog.String("component", req.Component), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "KEY_CREATE_FAILED", "failed to validate component")
		return
	}

	key, err := h.Store.CreateKey(r.Context(), req.AccountID, req.Component, req.Label, req.ExpiresAt)
	if err != nil {
		if errors.Is(err, store.ErrAccountNotFound) {
			// Narrow race: account removed between the GetAccount check and the insert.
			writeError(w, http.StatusNotFound, "ACCOUNT_NOT_FOUND",
				fmt.Sprintf("account %q not found", req.AccountID))
			return
		}
		h.Logger.Error("failed to create key", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "KEY_CREATE_FAILED", "failed to create key")
		return
	}

	audit.WriteFromRequest(r.Context(), h.Auditor, r, audit.Entry{
		Action:     "key.issue",
		TargetType: "key",
		TargetID:   key.ID,
		Details: map[string]any{
			"account_id": key.AccountID,
			"component":  key.Component,
			// Operator-controllable, 1 MiB body cap is too loose for an
			// audit_log row — bound at AuditFieldMaxLen so attacker can't
			// bloat the audit table.
			"label": middleware.TruncateAuditField(key.Label),
		},
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(h.wrapKey(key))
}
