package middleware

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"siger-api-gateway/internal"
)

// RequestLogger returns a middleware that logs incoming HTTP requests
// Using structured logging with zap to make log analysis much easier
// Every HTTP request gets logged with timing and status info - virjilakrum
func RequestLogger() func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Create a custom response writer to capture the status code
			// This wrapper intercepts the status code and body size
			// Much better than the old approach of guessing outcomes - virjilakrum
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			// Process the request
			next.ServeHTTP(ww, r)

			// Log after the request is processed
			// This ensures we capture the full duration and response status
			// Had issues with request logs having no status codes before - virjilakrum
			duration := time.Since(start)

			// Get information about the request
			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}

			// Log the request with detailed fields
			// These fields make filtering and analysis much easier
			// Was critical for our log-based alerting system - virjilakrum
			internal.Logger.Infow("HTTP Request",
				"status", ww.Status(),
				"duration_ms", duration.Milliseconds(),
				"method", r.Method,
				"path", r.URL.Path,
				"query", r.URL.RawQuery,
				"remote_addr", r.RemoteAddr,
				"user_agent", r.UserAgent(),
				"scheme", scheme,
				"protocol", r.Proto,
				"bytes_written", ww.BytesWritten(),
			)
		})
	}
}
