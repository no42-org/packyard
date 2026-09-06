/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

// Package metrics defines Prometheus metrics for the packyard-auth service.
package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	// RequestsTotal counts forwardAuth requests by outcome status.
	// Labels: status = allowed | denied | error
	RequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "packyard_auth_requests_total",
			Help: "Total number of forwardAuth requests by status.",
		},
		[]string{"status"},
	)

	// RequestDuration measures forwardAuth handler latency in seconds.
	RequestDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "packyard_auth_duration_seconds",
			Help:    "ForwardAuth request duration in seconds.",
			Buckets: prometheus.DefBuckets,
		},
	)

	// ComponentCacheRequests counts every component lookup that passed through
	// the public-component cache, hit or miss. Hit ratio is
	// ComponentCacheHits / ComponentCacheRequests.
	ComponentCacheRequests = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "packyard_auth_component_cache_requests_total",
			Help: "Total number of component lookups through the public-component cache.",
		},
	)

	// ComponentCacheHits counts component lookups answered from the cache
	// without a store call.
	ComponentCacheHits = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "packyard_auth_component_cache_hits_total",
			Help: "Total number of component lookups served from the public-component cache.",
		},
	)

	// ComponentCacheEntries is the number of components currently cached as
	// public. It should track the number of public components; growth beyond
	// that indicates a bug.
	ComponentCacheEntries = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "packyard_auth_component_cache_entries",
			Help: "Number of components currently held in the public-component cache.",
		},
	)
)

func init() {
	prometheus.MustRegister(RequestsTotal, RequestDuration,
		ComponentCacheRequests, ComponentCacheHits, ComponentCacheEntries)
}
