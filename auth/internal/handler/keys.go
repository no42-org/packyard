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
	"github.com/opennms/packyard-auth/internal/store"
)

// validComponents is the authoritative set of component values accepted by the admin API.
var validComponents = map[string]bool{
	"core":     true,
	"minion":   true,
	"sentinel": true,
}

// KeysHandler handles admin API key management endpoints (Epic 3).
type KeysHandler struct {
	Store  store.KeyStore
	Logger *slog.Logger
}

// createKeyRequest is the JSON body for POST /api/v1/keys.
type createKeyRequest struct {
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
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(key)
}

// Delete handles DELETE /api/v1/keys/{id} — revokes a key immediately.
// Returns 204 on success or if the key is already revoked (idempotent).
// Returns 404 if the key ID does not exist at all.
func (h *KeysHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	err := h.Store.RevokeKey(r.Context(), id)
	if err == nil {
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
			// Key exists but is already revoked — idempotent 204.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if errors.Is(getErr, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "KEY_NOT_FOUND",
				fmt.Sprintf("key %q not found", id))
			return
		}
		h.Logger.Error("failed to get key after revoke", slog.String("id", id), slog.String("error", getErr.Error()))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	h.Logger.Error("failed to revoke key", slog.String("id", id), slog.String("error", err.Error()))
	w.WriteHeader(http.StatusInternalServerError)
}

// List handles GET /api/v1/keys — returns all keys, optionally filtered by ?component=.
func (h *KeysHandler) List(w http.ResponseWriter, r *http.Request) {
	component := r.URL.Query().Get("component")
	if component != "" && !validComponents[component] {
		writeError(w, http.StatusBadRequest, "INVALID_COMPONENT",
			fmt.Sprintf("component %q is not valid; must be one of: core, minion, sentinel", component))
		return
	}

	keys, err := h.Store.ListKeys(r.Context(), component)
	if err != nil {
		h.Logger.Error("failed to list keys", slog.String("error", err.Error()))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// nil slice encodes as JSON null — return [] for empty result (AC4).
	if keys == nil {
		keys = []*store.Key{}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(keys)
}

// Create handles POST /api/v1/keys — provisions a new component-scoped subscription key.
func (h *KeysHandler) Create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB
	var req createKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body must be valid JSON")
		return
	}

	if !validComponents[req.Component] {
		writeError(w, http.StatusBadRequest, "INVALID_COMPONENT",
			fmt.Sprintf("component %q is not valid; must be one of: core, minion, sentinel", req.Component))
		return
	}

	key, err := h.Store.CreateKey(r.Context(), req.Component, req.Label, req.ExpiresAt)
	if err != nil {
		h.Logger.Error("failed to create key", slog.String("error", err.Error()))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(key)
}
