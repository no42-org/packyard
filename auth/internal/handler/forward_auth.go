// Package handler contains HTTP handlers for the packyard-auth service.
package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/no42-org/packyard-auth/internal/metrics"
	"github.com/no42-org/packyard-auth/internal/store"
)

// ForwardAuthHandler validates subscriber credentials for Traefik forwardAuth.
// GET /auth — returns 200 (allow), 401 (deny), or 503 (error/fail-closed).
// Construct with NewForwardAuthHandler to guarantee non-nil component maps.
type ForwardAuthHandler struct {
	Store            store.KeyStore
	Logger           *slog.Logger
	ValidComponents  map[string]bool // set for O(1) membership checks
	PublicComponents map[string]bool // components that bypass credential checking
}

// NewForwardAuthHandler returns a ForwardAuthHandler with nil maps coerced to
// empty maps so that component lookups in ServeHTTP never misbehave silently.
func NewForwardAuthHandler(st store.KeyStore, logger *slog.Logger, validComponents, publicComponents map[string]bool) *ForwardAuthHandler {
	if validComponents == nil {
		validComponents = map[string]bool{}
	}
	if publicComponents == nil {
		publicComponents = map[string]bool{}
	}
	return &ForwardAuthHandler{
		Store:            st,
		Logger:           logger,
		ValidComponents:  validComponents,
		PublicComponents: publicComponents,
	}
}

// ServeHTTP implements http.Handler.
// Response body is always empty — package managers do not parse bodies on auth failure.
// The Authorization header value is never logged at any level (NFR5).
func (h *ForwardAuthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() {
		metrics.RequestDuration.Observe(time.Since(start).Seconds())
	}()

	// Resolve component to check for public bypass before any credential work.
	requestedComponent, ok := extractComponent(r.Header.Get("X-Forwarded-Uri"))
	if !ok {
		metrics.RequestsTotal.WithLabelValues("denied").Inc()
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Public components bypass credential checking entirely.
	if h.PublicComponents[requestedComponent] {
		h.Logger.Info("public component access allowed", slog.String("component", requestedComponent))
		metrics.RequestsTotal.WithLabelValues("allowed-public").Inc()
		w.WriteHeader(http.StatusOK)
		return
	}

	// Private component: require valid credentials.
	// Parse HTTP Basic Auth — r.BasicAuth() handles RFC 7235 decoding correctly.
	// The username is ignored; the password IS the subscription key value.
	_, password, ok := r.BasicAuth()
	if !ok || len(password) != 64 || !isHex(password) {
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

	// 404 only visible to authenticated callers — prevents component enumeration by unauthenticated actors.
	if !h.ValidComponents[requestedComponent] {
		metrics.RequestsTotal.WithLabelValues("denied").Inc()
		w.WriteHeader(http.StatusNotFound)
		return
	}

	if key.Component != requestedComponent {
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

// isHex reports whether s consists entirely of hexadecimal characters [0-9a-fA-F].
// Used to fast-reject non-hex key values before a store lookup.
func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// extractComponent parses the LTS component name from an X-Forwarded-Uri path.
//
// Supported formats:
//
//	/rpm/{component}/{series}/{os-arch}/...   → component at index 1
//	/deb/{component}/{series}/...             → component at index 1
//	/oci/v2/lts-{component}/...               → strip "lts-" prefix from index 2
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
		// /rpm/{component}/{series}/{os-arch}/...
		return parts[1], true
	case "deb":
		// /deb/{component}/{series}/...
		return parts[1], true
	case "oci":
		// /oci/v2/lts-{component}/...
		if len(parts) < 3 {
			return "", false
		}
		after, found := strings.CutPrefix(parts[2], "lts-")
		if !found {
			return "", false
		}
		return after, true
	}
	return "", false
}
