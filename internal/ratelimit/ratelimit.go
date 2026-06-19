// Package ratelimit implements a token bucket rate limiter for webhook endpoints.
//
// Per-endpoint rate limiting with configurable requests-per-minute and burst.
// Uses sync.Mutex for thread safety.
package ratelimit

import (
	"sync"
	"time"
)

// Limiter is a token bucket rate limiter that tracks request rates per key.
// Safe for concurrent use.
type Limiter struct {
	rate       float64          // tokens per second
	burst      int              // max tokens per bucket
	buckets    map[string]*bucket
	mu         sync.Mutex
	cleanupTTL time.Duration
}

type bucket struct {
	tokens   float64
	lastTime time.Time
}

// New creates a new Limiter with the given requests per minute and burst size.
// requestsPerMinute: maximum sustained request rate per key
// burst: maximum burst size (tokens available immediately)
func New(requestsPerMinute, burst int) *Limiter {
	if burst <= 0 {
		burst = 1
	}
	return &Limiter{
		rate:       float64(requestsPerMinute) / 60.0,
		burst:      burst,
		buckets:    make(map[string]*bucket),
		cleanupTTL: 5 * time.Minute,
	}
}

// Allow checks if a request identified by key is allowed.
// Returns a Result with whether the request is permitted.
func (l *Limiter) Allow(key string) Result {
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	now := time.Now()
	if !ok {
		b = &bucket{tokens: float64(l.burst), lastTime: now}
		l.buckets[key] = b
	} else {
		elapsed := now.Sub(b.lastTime).Seconds()
		b.tokens += elapsed * l.rate
		if b.tokens > float64(l.burst) {
			b.tokens = float64(l.burst)
		}
		b.lastTime = now
	}

	if b.tokens >= 1.0 {
		b.tokens -= 1.0
		return Result{Allowed: true}
	}
	return Result{Allowed: false}
}

// Result holds the outcome of an Allow() call.
type Result struct {
	Allowed bool
}

// RateLimitError is returned when a request exceeds the rate limit.
type RateLimitError struct {
	Message string
}

func (e *RateLimitError) Error() string {
	return e.Message
}
