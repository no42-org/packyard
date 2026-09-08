/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

// Package handler contains HTTP handlers for the packyard-auth service.
package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/no42-org/packyard-auth/internal/logsafe"

	"github.com/no42-org/packyard-auth/internal/metrics"
	"github.com/no42-org/packyard-auth/internal/store"
)

// ForwardAuthHandler validates subscriber credentials for Traefik forwardAuth.
// GET /auth — returns 200 (allow), 401 (deny), 404 (path shape not served),
// 405 (write method on a subscriber path), or 503 (error/fail-closed).
// Component visibility is resolved via a live DB lookup on every request so that
// visibility changes take effect immediately without a service restart.
//
// Per change 2026-05-21-admin-ui-account-lifecycle § 2.5/D11, a suspended or
// deleted owning account also denies the request without mutating the key row,
// so reactivation restores subscriber access immediately.
type ForwardAuthHandler struct {
	Store          store.KeyStore
	ComponentStore store.ComponentStore
	AccountStore   store.AccountStore
	Logger         *slog.Logger
}

// NewForwardAuthHandler returns a ForwardAuthHandler backed by the given stores.
// AccountStore is required — it carries the D11 suspended/deleted-account gate;
// a nil arg would silently re-open access for suspended subscribers.
func NewForwardAuthHandler(st store.KeyStore, cs store.ComponentStore, as store.AccountStore, logger *slog.Logger) *ForwardAuthHandler {
	if as == nil {
		panic("handler: NewForwardAuthHandler requires a non-nil AccountStore (D11 gate)")
	}
	return &ForwardAuthHandler{
		Store:          st,
		ComponentStore: cs,
		AccountStore:   as,
		Logger:         logger,
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

	// Subscribers only read. The Traefik routers already refuse write methods,
	// but forward-auth is the second line: a new route or an edited rule must
	// not silently re-open writes into the serving containers, none of which
	// authenticate on their own. Traefik puts the original method in
	// X-Forwarded-Method; when the header is absent, the request method is the
	// only thing to go on.
	method := r.Header.Get("X-Forwarded-Method")
	if method == "" {
		method = r.Method
	}
	if method != http.MethodGet && method != http.MethodHead {
		h.Logger.Warn("write method refused on a subscriber path", logsafe.Attr("method", method))
		metrics.RequestsTotal.WithLabelValues("denied-method").Inc()
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// A path matching none of the served shapes carries no component to reason
	// about. 404 says so; 401 would tell a subscriber their key is wrong when
	// their path is. Unknown components below keep answering 401, which is
	// what the enumeration guard is for.
	requestedComponent, ok := extractComponent(r.Header.Get("X-Forwarded-Uri"))
	if !ok {
		metrics.RequestsTotal.WithLabelValues("denied-path").Inc()
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// Live DB lookup — resolves visibility without requiring a restart after PATCH.
	// Unknown components return 401 (not 404) to prevent component enumeration by
	// unauthenticated callers.
	comp, err := h.ComponentStore.GetComponent(r.Context(), requestedComponent)
	if err != nil {
		if errors.Is(err, store.ErrComponentNotFound) {
			metrics.RequestsTotal.WithLabelValues("denied").Inc()
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		h.Logger.Error("store error resolving component",
			logsafe.Attr("component", requestedComponent),
			slog.String("error", err.Error()),
		)
		metrics.RequestsTotal.WithLabelValues("error").Inc()
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	// Public components bypass credential checking entirely.
	if comp.Visibility == "public" {
		h.Logger.Info("public component access allowed", logsafe.Attr("component", requestedComponent))
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

	// Component existence already confirmed by GetComponent above.
	if key.Component != requestedComponent {
		metrics.RequestsTotal.WithLabelValues("denied").Inc()
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// Account-level gate (D11): suspended or deleted accounts deny subscriber
	// access without touching the key row, so reactivation restores access
	// immediately and no subscriber redeployment is needed.
	if h.AccountStore != nil {
		account, err := h.AccountStore.GetAccount(r.Context(), key.AccountID)
		if err != nil {
			if errors.Is(err, store.ErrAccountNotFound) {
				// Covers both missing and soft-deleted accounts.
				metrics.RequestsTotal.WithLabelValues("denied").Inc()
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			h.Logger.Error("store error resolving account",
				slog.String("account_id", key.AccountID),
				slog.String("error", err.Error()),
			)
			metrics.RequestsTotal.WithLabelValues("error").Inc()
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if account.Status != store.AccountStatusActive {
			metrics.RequestsTotal.WithLabelValues("denied").Inc()
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
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
//	/oci/v2/{component}/{image}/...           → component at index 2
//
// The component is the name it carries in the admin UI in all three, with no
// prefix added or stripped. Registry repositories used to be named
// lts-{component}; a path in that shape now denotes a component literally
// named "lts-{component}", which is normally unknown and therefore denied.
//
// Returns ("", false) if the path is unrecognised or too short. Callers answer
// 404 for that, not 401: a path shape reveals nothing about which components
// exist, so hiding it behind the enumeration guard only misleads.
func extractComponent(path string) (string, bool) {
	// TrimPrefix removes the leading slash so SplitN gives clean segments.
	parts := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 5)
	if len(parts) < 2 || parts[0] == "" {
		return "", false
	}
	var comp string
	switch parts[0] {
	case "rpm", "deb":
		// /rpm/{component}/{series}/{os-arch}/...
		// /deb/{component}/{series}/...
		comp = parts[1]
	case "oci":
		// /oci/v2/{component}/{image}/... — Traefik only ever forwards the
		// /oci/v2/ shape, so anything else under /oci/ is not a served path.
		if len(parts) < 3 || parts[1] != "v2" {
			return "", false
		}
		comp = parts[2]
	default:
		return "", false
	}
	// An empty segment (e.g. "/rpm//x" or "/oci/v2//x") is not a component.
	if comp == "" {
		return "", false
	}
	return comp, true
}
