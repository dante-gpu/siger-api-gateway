package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"siger-api-gateway/internal/metrics"
)

// Metrics returns a middleware that records request metrics
func Metrics() func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Track in-flight requests
			metrics.GatewayInFlightRequests.Inc()
			defer metrics.GatewayInFlightRequests.Dec()

			// Create a custom response writer to capture the status code and body size
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			// Process the request
			next.ServeHTTP(ww, r)

			// Capture metrics after processing
			duration := time.Since(start).Seconds()
			statusCode := strconv.Itoa(ww.Status())

			// TODO:Use the URL path for metrics
			path := r.URL.Path

			// Record request count
			metrics.HTTPRequestsTotal.WithLabelValues(statusCode, r.Method, path).Inc()

			// Record request duration
			metrics.HTTPRequestDuration.WithLabelValues(r.Method, path).Observe(duration)

			// Record response size
			metrics.HTTPResponseSize.WithLabelValues(r.Method, path).Observe(float64(ww.BytesWritten()))
		})
	}
}
