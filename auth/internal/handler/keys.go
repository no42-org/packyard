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
	"github.com/no42-org/packyard-auth/internal/store"
)

// KeysHandler handles admin API key management endpoints (Epic 3).
type KeysHandler struct {
	Store              store.KeyStore
	Logger             *slog.Logger
	ValidComponents    map[string]bool   // set for O(1) membership checks
	ValidComponentList string            // pre-formatted for error messages
	ComponentVisibility map[string]string // component name → "public" | "private"
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
	_ = json.NewEncoder(w).Encode(h.wrapKey(key))
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
	if component != "" && !h.ValidComponents[component] {
		writeError(w, http.StatusBadRequest, "INVALID_COMPONENT",
			fmt.Sprintf("component %q is not valid; must be one of: %s", component, h.ValidComponentList))
		return
	}

	keys, err := h.Store.ListKeys(r.Context(), component)
	if err != nil {
		h.Logger.Error("failed to list keys", slog.String("error", err.Error()))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	wrapped := make([]*keyResponse, len(keys))
	for i, k := range keys {
		wrapped[i] = h.wrapKey(k)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(wrapped)
}

// Create handles POST /api/v1/keys — provisions a new component-scoped subscription key.
func (h *KeysHandler) Create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MiB
	var req createKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body must be valid JSON")
		return
	}

	if !h.ValidComponents[req.Component] {
		writeError(w, http.StatusBadRequest, "INVALID_COMPONENT",
			fmt.Sprintf("component %q is not valid; must be one of: %s", req.Component, h.ValidComponentList))
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
	_ = json.NewEncoder(w).Encode(h.wrapKey(key))
}
