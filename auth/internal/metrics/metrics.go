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
)

func init() {
	prometheus.MustRegister(RequestsTotal, RequestDuration)
}
