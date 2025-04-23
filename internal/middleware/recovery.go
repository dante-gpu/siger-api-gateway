package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"siger-api-gateway/internal"
)

// Recoverer is a middleware that recovers from panics, logs the panic, and returns a 500 error
func Recoverer() func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rvr := recover(); rvr != nil {
					// Log the stack trace
					stackTrace := string(debug.Stack())
					internal.Logger.Errorw("Panic recovered",
						"panic", fmt.Sprintf("%v", rvr),
						"stack", stackTrace,
						"path", r.URL.Path,
						"method", r.Method,
						"remote_addr", r.RemoteAddr,
					)

					// Return a 500 Internal Server Error
					w.WriteHeader(http.StatusInternalServerError)
					w.Write([]byte("Internal Server Error"))
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}
