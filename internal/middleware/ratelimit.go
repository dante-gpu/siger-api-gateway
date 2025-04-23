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
func (rl *RateLimiter) janitor() {
	ticker := time.NewTicker(rl.ttl)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		keysToDelete := []string{}

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
func TokenBucketRateLimit(rps rate.Limit, burst int) func(next http.Handler) http.Handler {
	// Create a new rate limiter with 1 hour TTL
	rateLimiter := NewRateLimiter(rps, burst, 1*time.Hour)
	return RateLimit(rateLimiter)
}
