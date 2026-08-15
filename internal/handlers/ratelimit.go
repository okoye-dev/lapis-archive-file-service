package handlers

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/okoye-dev/lapis-archive-file-service/internal/transport/rest"
)

// RateLimitByIP throttles a route group per client IP.
func RateLimitByIP(limit int, window time.Duration) gin.HandlerFunc {
	limiter := newRateLimiter(limit, window)
	return func(c *gin.Context) {
		if !limiter.Allow(c.ClientIP()) {
			rest.Error(c, http.StatusTooManyRequests, "Too many requests, slow down")
			c.Abort()
			return
		}
		c.Next()
	}
}

type rateLimiter struct {
	mu      sync.Mutex
	events  map[string][]time.Time
	limit   int
	window  time.Duration
	maxKeys int
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		events:  make(map[string][]time.Time),
		limit:   limit,
		window:  window,
		maxKeys: 10000,
	}
}

func (r *rateLimiter) Allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-r.window)

	kept := r.events[key][:0]
	for _, t := range r.events[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}

	if len(kept) >= r.limit {
		r.events[key] = kept
		return false
	}

	if _, exists := r.events[key]; !exists && len(r.events) >= r.maxKeys {
		r.evictStale(cutoff)
		if len(r.events) >= r.maxKeys {
			return false
		}
	}

	r.events[key] = append(kept, now)
	return true
}

func (r *rateLimiter) evictStale(cutoff time.Time) {
	for k, times := range r.events {
		if len(times) == 0 || !times[len(times)-1].After(cutoff) {
			delete(r.events, k)
		}
	}
}
