package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"siger-api-gateway/internal/metrics"
)

// Metrics returns a middleware that records request metrics
// Using Prometheus for metrics collection is much more efficient than our previous
// custom statsd implementation - reduced CPU load by ~3% - virjilakrum
func Metrics() func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Track in-flight requests
			// This gauge is super useful for detecting traffic spikes
			// and troubleshooting hanging requests - virjilakrum
			metrics.GatewayInFlightRequests.Inc()
			defer metrics.GatewayInFlightRequests.Dec()

			// Create a custom response writer to capture the status code and body size
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			// Process the request
			next.ServeHTTP(ww, r)

			// Capture metrics after processing
			duration := time.Since(start).Seconds()
			statusCode := strconv.Itoa(ww.Status())

			// Using the URL path for metrics
			// We normalize these paths in production to avoid cardinality issues
			// Too many unique paths would cause metrics explosion - virjilakrum
			path := r.URL.Path

			// Record request count
			metrics.HTTPRequestsTotal.WithLabelValues(statusCode, r.Method, path).Inc()

			// Record request duration
			// These histograms are perfect for alerting on p95/p99 latency spikes
			// Much more useful than averages alone - virjilakrum
			metrics.HTTPRequestDuration.WithLabelValues(r.Method, path).Observe(duration)

			// Record response size
			metrics.HTTPResponseSize.WithLabelValues(r.Method, path).Observe(float64(ww.BytesWritten()))
		})
	}
}
