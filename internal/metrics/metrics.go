package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// HTTPRequestsTotal counts total HTTP requests
	HTTPRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests by status code, method, and path",
		},
		[]string{"status", "method", "path"},
	)

	// HTTPRequestDuration observes HTTP request duration
	HTTPRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "Duration of HTTP requests in seconds",
			Buckets: []float64{0.001, 0.01, 0.1, 0.5, 1, 2, 5, 10},
		},
		[]string{"method", "path"},
	)

	// HTTPResponseSize observes HTTP response sizes
	HTTPResponseSize = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_response_size_bytes",
			Help:    "Size of HTTP responses in bytes",
			Buckets: []float64{100, 1000, 10000, 100000, 1000000},
		},
		[]string{"method", "path"},
	)

	// GatewayInFlightRequests tracks in-flight requests
	GatewayInFlightRequests = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "gateway_in_flight_requests",
			Help: "Number of requests currently being processed by the gateway",
		},
	)

	// UpstreamRequestsTotal tracks requests to upstream services
	UpstreamRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gateway_upstream_requests_total",
			Help: "Total number of requests to upstream services",
		},
		[]string{"service", "status"},
	)

	// UpstreamRequestDuration observes duration of upstream requests
	UpstreamRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gateway_upstream_request_duration_seconds",
			Help:    "Duration of upstream service requests in seconds",
			Buckets: []float64{0.001, 0.01, 0.1, 0.5, 1, 2, 5, 10},
		},
		[]string{"service"},
	)
)
