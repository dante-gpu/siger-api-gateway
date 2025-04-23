package middleware

import (
	"net/http"
	"strings"
)

// CORS middleware options
type CORSOptions struct {
	AllowedOrigins   []string // List of allowed origins
	AllowedMethods   []string // List of allowed HTTP methods
	AllowedHeaders   []string // List of allowed headers
	ExposedHeaders   []string // List of headers that can be exposed to the client
	AllowCredentials bool     // Whether to allow credentials
	MaxAge           int      // How long preflight request can be cached (in seconds)
}

// DefaultCORSOptions returns the default CORS options
func DefaultCORSOptions() *CORSOptions {
	return &CORSOptions{
		AllowedOrigins:   []string{"*"}, // Allow all origins
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token", "X-Request-ID"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300, // 5 minutes
	}
}

// CORS returns a middleware that handles CORS
func CORS(options *CORSOptions) func(next http.Handler) http.Handler {
	if options == nil {
		options = DefaultCORSOptions()
	}

	allowedOriginsAll := options.AllowedOrigins[0] == "*"
	allowedOrigins := make(map[string]bool)
	for _, origin := range options.AllowedOrigins {
		allowedOrigins[strings.ToLower(origin)] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				// Not a CORS request
				next.ServeHTTP(w, r)
				return
			}

			// Check if the origin is allowed
			originAllowed := allowedOriginsAll || allowedOrigins[strings.ToLower(origin)]
			if !originAllowed {
				next.ServeHTTP(w, r)
				return
			}

			// Set CORS headers
			if allowedOriginsAll {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}

			// Handle preflight OPTIONS request
			if r.Method == "OPTIONS" {
				w.Header().Set("Access-Control-Allow-Methods", strings.Join(options.AllowedMethods, ", "))
				w.Header().Set("Access-Control-Allow-Headers", strings.Join(options.AllowedHeaders, ", "))
				w.Header().Set("Access-Control-Expose-Headers", strings.Join(options.ExposedHeaders, ", "))
				if options.AllowCredentials {
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				}
				if options.MaxAge > 0 {
					w.Header().Set("Access-Control-Max-Age", string(options.MaxAge))
				}
				w.WriteHeader(http.StatusNoContent) // 204 No Content
				return
			}

			// For non-OPTIONS requests, just add the exposed headers
			w.Header().Set("Access-Control-Expose-Headers", strings.Join(options.ExposedHeaders, ", "))
			if options.AllowCredentials {
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}

			next.ServeHTTP(w, r)
		})
	}
}
