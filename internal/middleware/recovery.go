package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"siger-api-gateway/internal"
)

// Recoverer is a middleware that recovers from panics, logs the panic, and returns a 500 error
// This is our safety net against unhandled panics across all routes
// Must be registered first in the middleware chain to catch everything - virjilakrum
func Recoverer() func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rvr := recover(); rvr != nil {
					// Log the stack trace
					// Capturing the full stack trace is essential for debugging production issues
					// Saved us countless hours tracking down hard-to-reproduce bugs - virjilakrum
					stackTrace := string(debug.Stack())
					internal.Logger.Errorw("Panic recovered",
						"panic", fmt.Sprintf("%v", rvr),
						"stack", stackTrace,
						"path", r.URL.Path,
						"method", r.Method,
						"remote_addr", r.RemoteAddr,
					)

					// Return a 500 Internal Server Error
					// Deliberately not exposing panic details to client for security
					// Pre-production we used to return stack traces but that was an information leak - virjilakrum
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte("Internal Server Error"))
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}
