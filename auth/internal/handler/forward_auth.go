// Package handler contains HTTP handlers for the packyard-auth service.
package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/opennms/packyard-auth/internal/metrics"
	"github.com/opennms/packyard-auth/internal/store"
)

// ForwardAuthHandler validates subscriber credentials for Traefik forwardAuth.
// GET /auth — returns 200 (allow), 401 (deny), or 503 (error/fail-closed).
type ForwardAuthHandler struct {
	Store  store.KeyStore
	Logger *slog.Logger
}

// ServeHTTP implements http.Handler.
// Response body is always empty — package managers do not parse bodies on auth failure.
// The Authorization header value is never logged at any level (NFR5).
func (h *ForwardAuthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() {
		metrics.RequestDuration.Observe(time.Since(start).Seconds())
	}()

	// Parse HTTP Basic Auth — r.BasicAuth() handles RFC 7235 decoding correctly.
	// The username is ignored; the password IS the subscription key value.
	_, password, ok := r.BasicAuth()
	if !ok || len(password) != 64 {
		metrics.RequestsTotal.WithLabelValues("denied").Inc()
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	key, err := h.Store.GetByValue(r.Context(), password)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrRevoked) {
			metrics.RequestsTotal.WithLabelValues("denied").Inc()
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// Unexpected store error — fail closed (NFR11): never return 200 on error.
		h.Logger.Error("store error in forwardAuth", slog.String("error", err.Error()))
		metrics.RequestsTotal.WithLabelValues("error").Inc()
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	requestedComponent, ok := extractComponent(r.Header.Get("X-Forwarded-Uri"))
	if !ok || key.Component != requestedComponent {
		metrics.RequestsTotal.WithLabelValues("denied").Inc()
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Increment usage counter — fire-and-forget, never deny request on failure.
	if err := h.Store.IncrementUsage(r.Context(), key.ID); err != nil {
		h.Logger.Warn("failed to increment usage",
			slog.String("key_id", key.ID),
			slog.String("error", err.Error()),
		)
	}

	metrics.RequestsTotal.WithLabelValues("allowed").Inc()
	w.WriteHeader(http.StatusOK)
}

// extractComponent parses the Meridian component name from an X-Forwarded-Uri path.
//
// Supported formats:
//
//	/rpm/{os-arch}/{component}/{year}/...   → component at index 2
//	/deb/{component}/{year}/...             → component at index 1
//	/oci/v2/meridian-{component}/...        → strip "meridian-" prefix from index 2
//
// Returns ("", false) if the path is unrecognised or too short.
func extractComponent(path string) (string, bool) {
	// TrimPrefix removes the leading slash so SplitN gives clean segments.
	parts := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 5)
	if len(parts) < 2 || parts[0] == "" {
		return "", false
	}
	switch parts[0] {
	case "rpm":
		// /rpm/{os-arch}/{component}/{year}/...
		if len(parts) < 3 {
			return "", false
		}
		return parts[2], true
	case "deb":
		// /deb/{component}/{year}/...
		return parts[1], true
	case "oci":
		// /oci/v2/meridian-{component}/...
		if len(parts) < 3 {
			return "", false
		}
		after, found := strings.CutPrefix(parts[2], "meridian-")
		if !found {
			return "", false
		}
		return after, true
	}
	return "", false
}
