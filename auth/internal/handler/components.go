package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/no42-org/packyard-auth/internal/store"
)

// ComponentsHandler handles admin API component management endpoints.
// RPMDataRoot is the mount point of the rpm-data volume (default: /data/rpm).
// The RPM directory tree is created at {RPMDataRoot}/rpm/{component}/{series}/{os_family}-{arch}/.
type ComponentsHandler struct {
	Store       store.ComponentStore
	Logger      *slog.Logger
	RPMDataRoot string
}

// NewComponentsHandler returns a ComponentsHandler.
// rpmDataRoot defaults to /data/rpm when empty.
func NewComponentsHandler(st store.ComponentStore, logger *slog.Logger, rpmDataRoot string) *ComponentsHandler {
	if rpmDataRoot == "" {
		rpmDataRoot = "/data/rpm"
	}
	return &ComponentsHandler{Store: st, Logger: logger, RPMDataRoot: rpmDataRoot}
}

// createComponentRequest is the JSON body for POST /api/v1/components.
type createComponentRequest struct {
	Name             string   `json:"name"`
	Visibility       string   `json:"visibility"`
	RPMSeries        []string `json:"rpm_series"`
	RPMOSFamilies    []string `json:"rpm_os_families"`
	RPMArchitectures []string `json:"rpm_architectures"`
}

// deleteImpact is the body returned on DELETE without ?confirm.
type deleteImpact struct {
	Code    string       `json:"code"`
	Message string       `json:"message"`
	Impact  impactDetail `json:"impact"`
}

type impactDetail struct {
	KeysRevoked      int64    `json:"keys_revoked"`
	RPMSeriesRemoved []string `json:"rpm_series_removed"`
}

// deleteResult is the body returned on successful DELETE.
type deleteResult struct {
	KeysRevoked int64 `json:"keys_revoked"`
}

// Create handles POST /api/v1/components.
func (h *ComponentsHandler) Create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req createComponentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body must be valid JSON")
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "name is required")
		return
	}
	if strings.ContainsAny(req.Name, "/\\") || strings.Contains(req.Name, "..") {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST",
			"name must not contain path separators or traversal sequences")
		return
	}
	for _, field := range []struct {
		label  string
		values []string
	}{
		{"rpm_series", req.RPMSeries},
		{"rpm_os_families", req.RPMOSFamilies},
		{"rpm_architectures", req.RPMArchitectures},
	} {
		for _, v := range field.values {
			if strings.ContainsAny(v, "/\\") || strings.Contains(v, "..") {
				writeError(w, http.StatusBadRequest, "INVALID_REQUEST",
					fmt.Sprintf("%s contains invalid value %q", field.label, v))
				return
			}
		}
	}
	if req.Visibility != "" && req.Visibility != "public" && req.Visibility != "private" {
		writeError(w, http.StatusBadRequest, "INVALID_VISIBILITY",
			fmt.Sprintf("visibility %q is invalid; must be \"public\" or \"private\"", req.Visibility))
		return
	}

	comp := &store.Component{
		Name:             req.Name,
		Visibility:       req.Visibility,
		RPMSeries:        req.RPMSeries,
		RPMOSFamilies:    req.RPMOSFamilies,
		RPMArchitectures: req.RPMArchitectures,
	}
	if comp.RPMSeries == nil {
		comp.RPMSeries = []string{}
	}
	if comp.RPMOSFamilies == nil {
		comp.RPMOSFamilies = []string{}
	}
	if comp.RPMArchitectures == nil {
		comp.RPMArchitectures = []string{}
	}

	created, err := h.Store.CreateComponent(r.Context(), comp)
	if err != nil {
		if errors.Is(err, store.ErrComponentExists) {
			writeError(w, http.StatusConflict, "COMPONENT_EXISTS",
				fmt.Sprintf("component %q already exists", req.Name))
			return
		}
		h.Logger.Error("failed to create component", slog.String("name", req.Name), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "COMPONENT_CREATE_FAILED", "failed to create component")
		return
	}

	// Initialise RPM directory tree for each series × os_family × arch combination.
	if err := h.initRPMTree(r.Context(), created); err != nil {
		h.Logger.Error("RPM tree init failed, rolling back component",
			slog.String("name", created.Name), slog.String("error", err.Error()))
		if delErr := h.Store.DeleteComponent(r.Context(), created.Name); delErr != nil {
			h.Logger.Error("rollback failed", slog.String("name", created.Name), slog.String("error", delErr.Error()))
		}
		writeError(w, http.StatusInternalServerError, "RPM_INIT_FAILED",
			fmt.Sprintf("RPM directory initialisation failed: %s", err.Error()))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(created)
}

// List handles GET /api/v1/components.
func (h *ComponentsHandler) List(w http.ResponseWriter, r *http.Request) {
	comps, err := h.Store.ListComponents(r.Context())
	if err != nil {
		h.Logger.Error("failed to list components", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "COMPONENT_LIST_FAILED", "failed to list components")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(comps)
}

// GetOne handles GET /api/v1/components/{name}.
func (h *ComponentsHandler) GetOne(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	comp, err := h.Store.GetComponent(r.Context(), name)
	if err != nil {
		if errors.Is(err, store.ErrComponentNotFound) {
			writeError(w, http.StatusNotFound, "COMPONENT_NOT_FOUND",
				fmt.Sprintf("component %q not found", name))
			return
		}
		h.Logger.Error("failed to get component", slog.String("name", name), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "COMPONENT_GET_FAILED", "failed to get component")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(comp)
}

// Delete handles DELETE /api/v1/components/{name}.
// Without ?confirm={name} returns 409 with impact preview.
// With exact ?confirm={name} revokes keys and removes the component.
func (h *ComponentsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	comp, err := h.Store.GetComponent(r.Context(), name)
	if err != nil {
		if errors.Is(err, store.ErrComponentNotFound) {
			writeError(w, http.StatusNotFound, "COMPONENT_NOT_FOUND",
				fmt.Sprintf("component %q not found", name))
			return
		}
		h.Logger.Error("failed to get component for delete", slog.String("name", name), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "COMPONENT_DELETE_FAILED", "failed to get component for delete")
		return
	}

	activeKeys, err := h.Store.CountActiveComponentKeys(r.Context(), name)
	if err != nil {
		h.Logger.Error("failed to count keys", slog.String("name", name), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "COMPONENT_DELETE_FAILED", "failed to count keys")
		return
	}

	confirm := r.URL.Query().Get("confirm")
	if confirm != name {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(deleteImpact{
			Code: "CONFIRM_REQUIRED",
			Message: fmt.Sprintf(
				"Deleting %q will remove the component and revoke all associated keys. Pass ?confirm=%s to proceed.",
				name, name,
			),
			Impact: impactDetail{
				KeysRevoked:      activeKeys,
				RPMSeriesRemoved: comp.RPMSeries,
			},
		})
		return
	}

	// Atomically revoke all active keys and delete the component record (P3).
	revoked, err := h.Store.DeleteComponentWithRevoke(r.Context(), name)
	if err != nil {
		h.Logger.Error("failed to delete component", slog.String("name", name), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "COMPONENT_DELETE_FAILED", "failed to delete component")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(deleteResult{KeysRevoked: revoked})
}

// updateComponentRequest is the JSON body for PATCH /api/v1/components/{name}.
type updateComponentRequest struct {
	Visibility string `json:"visibility"`
}

// Update handles PATCH /api/v1/components/{name} — updates mutable component fields.
// Currently only visibility ("public" or "private") may be changed.
// The change is persisted immediately; forward-auth picks it up on the next request
// without a service restart.
func (h *ComponentsHandler) Update(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req updateComponentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body must be valid JSON")
		return
	}
	if req.Visibility != "public" && req.Visibility != "private" {
		writeError(w, http.StatusBadRequest, "INVALID_VISIBILITY",
			fmt.Sprintf("visibility %q is invalid; must be \"public\" or \"private\"", req.Visibility))
		return
	}

	comp, err := h.Store.UpdateComponentVisibility(r.Context(), name, req.Visibility)
	if err != nil {
		if errors.Is(err, store.ErrComponentNotFound) {
			writeError(w, http.StatusNotFound, "COMPONENT_NOT_FOUND",
				fmt.Sprintf("component %q not found", name))
			return
		}
		h.Logger.Error("failed to update component", slog.String("name", name), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "COMPONENT_UPDATE_FAILED", "failed to update component")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(comp)
}

// initRPMTree creates the RPM directory tree under RPMDataRoot for the component.
// Path structure: {RPMDataRoot}/rpm/{component}/{series}/{os_family}-{arch}/
func (h *ComponentsHandler) initRPMTree(ctx context.Context, comp *store.Component) error {
	for _, series := range comp.RPMSeries {
		for _, family := range comp.RPMOSFamilies {
			for _, arch := range comp.RPMArchitectures {
				dir := filepath.Join(h.RPMDataRoot, "rpm", comp.Name, series, family+"-"+arch)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return fmt.Errorf("mkdir %s: %w", dir, err)
				}
			}
		}
	}
	return nil
}
