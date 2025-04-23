package middleware

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"siger-api-gateway/internal"
)

// RequestLogger returns a middleware that logs incoming HTTP requests
func RequestLogger() func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Create a custom response writer to capture the status code
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			// Process the request
			next.ServeHTTP(ww, r)

			// Log after the request is processed
			duration := time.Since(start)

			// Get information about the request
			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}

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
