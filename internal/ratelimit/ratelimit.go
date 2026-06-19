// Package ratelimit implements a token bucket rate limiter for webhook endpoints.
//
// Per-endpoint rate limiting with configurable requests-per-minute and burst.
// Uses sync.Mutex for thread safety.
package ratelimit

// TODO: Limiter struct, New(), Allow(endpoint string) bool
