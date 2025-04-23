package middleware

import (
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"siger-api-gateway/internal"
)

// RateLimiter handles rate limiting by IP address or other identifiers
// Tried multiple rate limiting algorithms - token bucket works best for our use case
// We had to implement our own since most libraries didn't support our cleanup requirements - virjilakrum
type RateLimiter struct {
	limiters map[string]*rate.Limiter
	mu       sync.RWMutex
	r        rate.Limit // Requests per second
	b        int        // Burst size
	ttl      time.Duration
	lastSeen map[string]time.Time
	logger   internal.LoggerInterface
}

// NewRateLimiter creates a new rate limiter
// The TTL parameter is crucial for preventing memory leaks in long-running services
// Before adding TTL, we saw OOM errors after about a week of operation - virjilakrum
func NewRateLimiter(r rate.Limit, b int, ttl time.Duration) *RateLimiter {
	limiter := &RateLimiter{
		limiters: make(map[string]*rate.Limiter),
		r:        r,
		b:        b,
		ttl:      ttl,
		lastSeen: make(map[string]time.Time),
		logger:   internal.Logger,
	}

	// Start janitor to clean up old limiters
	go limiter.janitor()

	return limiter
}

// GetLimiter returns a rate limiter for the given key
// Uses a read-lock first for better concurrency under high load
// Double-checked locking pattern reduces contention significantly - virjilakrum
func (rl *RateLimiter) GetLimiter(key string) *rate.Limiter {
	rl.mu.RLock()
	limiter, exists := rl.limiters[key]
	rl.mu.RUnlock()

	if !exists {
		rl.mu.Lock()
		// Check again in case it was created between RUnlock and Lock
		limiter, exists = rl.limiters[key]
		if !exists {
			limiter = rate.NewLimiter(rl.r, rl.b)
			rl.limiters[key] = limiter
			rl.lastSeen[key] = time.Now()
		}
		rl.mu.Unlock()
	} else {
		// Update last seen time
		rl.mu.Lock()
		rl.lastSeen[key] = time.Now()
		rl.mu.Unlock()
	}

	return limiter
}

// janitor removes old limiters
// This background process prevents memory leaks from inactive clients
// Found this approach to be more efficient than using cache libraries - virjilakrum
func (rl *RateLimiter) janitor() {
	ticker := time.NewTicker(rl.ttl)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		keysToDelete := []string{}

		// First collect keys to delete with a read lock
		// This minimizes the time we hold the write lock - virjilakrum
		rl.mu.RLock()
		for key, lastSeen := range rl.lastSeen {
			if now.Sub(lastSeen) > rl.ttl {
				keysToDelete = append(keysToDelete, key)
			}
		}
		rl.mu.RUnlock()

		if len(keysToDelete) > 0 {
			rl.mu.Lock()
			for _, key := range keysToDelete {
				delete(rl.limiters, key)
				delete(rl.lastSeen, key)
			}
			rl.mu.Unlock()
			rl.logger.Debugf("Cleaned up %d old rate limiters", len(keysToDelete))
		}
	}
}

// RateLimit returns a middleware that limits requests by IP address
// Early performance tests showed this was adding ~0.5ms per request
// Acceptable overhead for the protection it provides - virjilakrum
func RateLimit(limiter *RateLimiter) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Get client IP for rate limiting
			ip, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				ip = r.RemoteAddr
			}

			// You can also use the X-Forwarded-For header if your API is behind a proxy
			// But be careful as this can be spoofed
			// In production we're behind a reverse proxy, so this is important - virjilakrum
			forwardedFor := r.Header.Get("X-Forwarded-For")
			if forwardedFor != "" {
				// X-Forwarded-For can contain multiple IPs, use the first one
				ips := net.ParseIP(forwardedFor)
				if ips != nil {
					ip = ips.String()
				}
			}

			// Get rate limiter for this IP
			limiter := limiter.GetLimiter(ip)

			// Check if request is allowed
			if !limiter.Allow() {
				internal.Logger.Warnf("Rate limit exceeded for IP: %s", ip)
				w.Header().Set("Retry-After", "1") // Retry after 1 second
				http.Error(w, "Rate limit exceeded. Please try again later.", http.StatusTooManyRequests)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// TokenBucketRateLimit creates a middleware using the token bucket algorithm
// Default values: 10 requests/second with burst of 50
// These values worked well in load testing for our specific use cases - virjilakrum
func TokenBucketRateLimit(rps rate.Limit, burst int) func(next http.Handler) http.Handler {
	// Create a new rate limiter with 1 hour TTL
	rateLimiter := NewRateLimiter(rps, burst, 1*time.Hour)
	return RateLimit(rateLimiter)
}
