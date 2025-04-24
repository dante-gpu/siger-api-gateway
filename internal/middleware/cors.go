package middleware

import (
	"net/http"
	"strconv"
	"strings"
)

// CORS middleware options
// Comprehensive options to handle different CORS requirements
// We need this flexibility for both web and mobile clients - virjilakrum
type CORSOptions struct {
	AllowedOrigins   []string // List of allowed origins
	AllowedMethods   []string // List of allowed HTTP methods
	AllowedHeaders   []string // List of allowed headers
	ExposedHeaders   []string // List of headers that can be exposed to the client
	AllowCredentials bool     // Whether to allow credentials
	MaxAge           int      // How long preflight request can be cached (in seconds)
}

// DefaultCORSOptions returns the default CORS options
// Somewhat permissive defaults for easier dev experience
// Production deployments should override these with stricter values - virjilakrum
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
// Fully implements the CORS spec for preflight requests and actual requests
// Added support for wildcard origins to simplify development - virjilakrum
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
			// Case insensitive matching for more robust handling
			// Had issues with mobile apps sending slightly different origin formats - virjilakrum
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
			// This is critical for browsers to allow the actual request
			// Must respond with 204 No Content for proper preflight - virjilakrum
			if r.Method == "OPTIONS" {
				w.Header().Set("Access-Control-Allow-Methods", strings.Join(options.AllowedMethods, ", "))
				w.Header().Set("Access-Control-Allow-Headers", strings.Join(options.AllowedHeaders, ", "))
				w.Header().Set("Access-Control-Expose-Headers", strings.Join(options.ExposedHeaders, ", "))
				if options.AllowCredentials {
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				}
				if options.MaxAge > 0 {
					w.Header().Set("Access-Control-Max-Age", strconv.Itoa(options.MaxAge))
				}
				w.WriteHeader(http.StatusNoContent) // 204 No Content
				return
			}

			// For non-OPTIONS requests, just add the exposed headers
			// This helps browsers know which headers they can access via JavaScript
			// Essential for tokens in headers and pagination links - virjilakrum
			w.Header().Set("Access-Control-Expose-Headers", strings.Join(options.ExposedHeaders, ", "))
			if options.AllowCredentials {
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}

			next.ServeHTTP(w, r)
		})
	}
}
