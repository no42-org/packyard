/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func TestMetricsEndpoint(t *testing.T) {
	// Seed one observation so the CounterVec produces output (Prometheus omits
	// label-dimensional metrics with zero observations from Gather output).
	RequestsTotal.WithLabelValues("allowed").Add(0)

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	promhttp.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "packyard_auth_requests_total") {
		t.Error("packyard_auth_requests_total not found in /metrics output")
	}
	if !strings.Contains(body, "packyard_auth_duration_seconds") {
		t.Error("packyard_auth_duration_seconds not found in /metrics output")
	}
}

func TestComponentCacheMetricsRegistered(t *testing.T) {
	// Touch each metric so it appears in Gather output even at zero.
	ComponentCacheRequests.Add(0)
	ComponentCacheHits.Add(0)
	ComponentCacheEntries.Set(0)

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	promhttp.Handler().ServeHTTP(w, req)
	body := w.Body.String()
	for _, name := range []string{
		"packyard_auth_component_cache_requests_total",
		"packyard_auth_component_cache_hits_total",
		"packyard_auth_component_cache_entries",
	} {
		if !strings.Contains(body, name) {
			t.Errorf("%s not found in /metrics output", name)
		}
	}
}
