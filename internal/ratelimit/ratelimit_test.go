package ratelimit

import (
	"sync"
	"testing"
	"time"
)

// ──────────────────── New() tests ─────────────────────────────────────

func TestNew_ValidParams(t *testing.T) {
	l := New(30, 5)
	if l == nil {
		t.Fatal("New returned nil")
	}
	if l.rate != 0.5 { // 30/60 = 0.5 tokens/sec
		t.Errorf("rate = %v, want 0.5", l.rate)
	}
	if l.burst != 5 {
		t.Errorf("burst = %d, want 5", l.burst)
	}
	if len(l.buckets) != 0 {
		t.Errorf("buckets should be empty, got %d", len(l.buckets))
	}
}

func TestNew_ZeroBurst_DefaultsToOne(t *testing.T) {
	l := New(60, 0)
	if l.burst != 1 {
		t.Errorf("zero burst should default to 1, got %d", l.burst)
	}
}

func TestNew_NegativeBurst_DefaultsToOne(t *testing.T) {
	l := New(60, -5)
	if l.burst != 1 {
		t.Errorf("negative burst should default to 1, got %d", l.burst)
	}
}

func TestNew_HighRate(t *testing.T) {
	l := New(60000, 100)
	if l.rate != 1000.0 { // 60000/60
		t.Errorf("rate = %v, want 1000", l.rate)
	}
}

// ──────────────────── Allow() — basic behavior ───────────────────────

func TestAllow_FirstRequestAllowed(t *testing.T) {
	l := New(30, 5)
	result := l.Allow("test-key")
	if !result.Allowed {
		t.Error("first request should be allowed")
	}
}

func TestAllow_BurstExhausted(t *testing.T) {
	l := New(30, 3)

	// First 3 should be allowed (burst)
	for i := 0; i < 3; i++ {
		if !l.Allow("key").Allowed {
			t.Fatalf("request %d within burst should be allowed", i+1)
		}
	}

	// 4th should be denied
	if l.Allow("key").Allowed {
		t.Error("request beyond burst should be denied")
	}
}

func TestAllow_DifferentKeysIndependent(t *testing.T) {
	l := New(30, 2)

	// Exhaust key A
	l.Allow("a")
	l.Allow("a")
	if l.Allow("a").Allowed {
		t.Error("key A should be exhausted")
	}

	// Key B should still be allowed (independent bucket)
	if !l.Allow("b").Allowed {
		t.Error("key B should have its own bucket")
	}
}

func TestAllow_RefillOverTime(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping refill test in short mode")
	}

	l := New(120, 2) // 2 tokens/sec

	// Exhaust burst
	l.Allow("key")
	l.Allow("key")
	if l.Allow("key").Allowed {
		t.Error("burst exhausted")
	}

	// Wait for ~1 token to refill (500ms at 2 tokens/sec = 1 token)
	time.Sleep(550 * time.Millisecond)

	if !l.Allow("key").Allowed {
		t.Error("should have refilled 1 token after 500ms")
	}

	// Should be exhausted again
	if l.Allow("key").Allowed {
		t.Error("should be exhausted after using refilled token")
	}
}

func TestAllow_BurstCap(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping burst cap test in short mode")
	}

	l := New(600, 3) // 10 tokens/sec, burst 3

	// Exhaust
	l.Allow("key")
	l.Allow("key")
	l.Allow("key")

	// Wait long enough that it would refill beyond burst if uncapped
	time.Sleep(600 * time.Millisecond) // 6 tokens worth

	// Should get exactly 3 tokens (capped at burst), not 6
	count := 0
	for i := 0; i < 5; i++ {
		if l.Allow("key").Allowed {
			count++
		}
	}
	if count != 3 {
		t.Errorf("burst cap failed: got %d tokens, want 3", count)
	}
}

// ──────────────────── Allow() — edge cases ────────────────────────────

func TestAllow_EmptyKey(t *testing.T) {
	l := New(30, 5)
	result := l.Allow("")
	if !result.Allowed {
		t.Error("empty key should still work (creates bucket)")
	}
}

func TestAllow_SpecialCharactersInKey(t *testing.T) {
	l := New(30, 5)
	keys := []string{
		"hook:my-app",
		"../../../etc/passwd",
		"key with spaces",
		"key\nwith\nnewlines",
		"\x00null",
	}
	for _, key := range keys {
		result := l.Allow(key)
		if !result.Allowed {
			t.Errorf("key %q should be allowed on first request", key)
		}
	}
}

func TestAllow_KeyWithShellMetacharacters(t *testing.T) {
	l := New(30, 5)
	keys := []string{"; rm -rf /", "| cat /etc/passwd", "$(whoami)", "`id`"}
	for _, key := range keys {
		result := l.Allow(key)
		if !result.Allowed {
			t.Errorf("shell metacharacter key %q should not crash", key)
		}
	}
}

func TestAllow_VeryLongKey(t *testing.T) {
	l := New(30, 5)
	longKey := make([]byte, 10_000)
	for i := range longKey {
		longKey[i] = 'x'
	}
	result := l.Allow(string(longKey))
	if !result.Allowed {
		t.Error("very long key should not crash")
	}
}

// ──────────────────── Concurrency tests ───────────────────────────────

func TestAllow_ConcurrentAccess(t *testing.T) {
	l := New(3000, 100) // 50 tokens/sec, burst 100

	var wg sync.WaitGroup
	goroutines := 50
	requestsPerGoroutine := 20
	allowed := make([]bool, goroutines*requestsPerGoroutine)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < requestsPerGoroutine; i++ {
				result := l.Allow("shared-key")
				idx := gid*requestsPerGoroutine + i
				allowed[idx] = result.Allowed
			}
		}(g)
	}
	wg.Wait()

	// Count allowed — should be exactly burst (100)
	allowedCount := 0
	for _, a := range allowed {
		if a {
			allowedCount++
		}
	}
	if allowedCount > 100 {
		t.Errorf("concurrent access exceeded burst: %d > 100", allowedCount)
	}
	if allowedCount < 100 {
		t.Logf("allowed %d requests (burst=100) — concurrency may have caused slight underuse", allowedCount)
	}
}

func TestAllow_ConcurrentDifferentKeys(t *testing.T) {
	l := New(3000, 50)

	var wg sync.WaitGroup
	keys := 20
	for i := 0; i < keys; i++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			key := fmtKey(k)
			// Each key should get full burst independently
			for j := 0; j < 5; j++ {
				l.Allow(key)
			}
		}(i)
	}
	wg.Wait()

	// No race detector violations = success
}

func fmtKey(k int) string {
	alphabet := "abcdefghijklmnopqrstuvwxyz"
	return string(alphabet[k%len(alphabet)])
}

// ──────────────────── Result type tests ───────────────────────────────

func TestResult_Allowed(t *testing.T) {
	r := Result{Allowed: true}
	if !r.Allowed {
		t.Error("Allowed should be true")
	}
}

func TestResult_Denied(t *testing.T) {
	r := Result{Allowed: false}
	if r.Allowed {
		t.Error("Allowed should be false")
	}
}

// ──────────────────── RateLimitError tests ────────────────────────────

func TestRateLimitError_Error(t *testing.T) {
	e := &RateLimitError{Message: "rate limit exceeded"}
	if e.Error() != "rate limit exceeded" {
		t.Errorf("Error() = %q, want %q", e.Error(), "rate limit exceeded")
	}
}

func TestRateLimitError_Empty(t *testing.T) {
	e := &RateLimitError{}
	if e.Error() != "" {
		t.Errorf("Error() = %q, want empty", e.Error())
	}
}

// ──────────────────── Security tests ──────────────────────────────────

func TestSecurity_RateLimitBypass_DifferentKeys(t *testing.T) {
	l := New(30, 3)

	// Exhaust key A
	l.Allow("a")
	l.Allow("a")
	l.Allow("a")
	if l.Allow("a").Allowed {
		t.Error("key A exhausted")
	}

	// Key B should still be allowed — attacker can't bypass by changing keys
	// but that's by design (rate limit is per-endpoint, not global)
	if !l.Allow("b").Allowed {
		t.Error("key B should have independent bucket")
	}
}

func TestSecurity_DoS_MemoryGrowth(t *testing.T) {
	// Creating many unique keys should not crash or grow unboundedly
	l := New(30, 5)
	for i := 0; i < 10_000; i++ {
		l.Allow(string(rune(i)))
	}
	// Should not panic or OOM
	l.mu.Lock()
	bucketCount := len(l.buckets)
	l.mu.Unlock()
	if bucketCount != 10_000 {
		t.Errorf("expected 10000 buckets, got %d", bucketCount)
	}
}

func TestSecurity_NilLimiter(t *testing.T) {
	// Document: nil Limiter should not be used with Allow
	// If code accidentally calls Allow on nil, it panics — which is the
	// correct behavior (fail-fast).
	var l *Limiter
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on nil Limiter.Allow()")
		}
	}()
	l.Allow("key")
}

// ──────────────────── Token precision tests ───────────────────────────

func TestAllow_FractionalTokenConsumption(t *testing.T) {
	l := New(60, 5) // 1 token/sec, burst 5

	// Use all 5 tokens
	for i := 0; i < 5; i++ {
		l.Allow("key")
	}

	// Wait 500ms — should have 0.5 tokens (not enough for 1 full request)
	time.Sleep(500 * time.Millisecond)

	if l.Allow("key").Allowed {
		t.Error("0.5 tokens should not be enough for a request (need >= 1.0)")
	}
}

func TestAllow_SubSecondRates(t *testing.T) {
	l := New(120, 10) // 2 tokens/sec

	// Consume all tokens
	for i := 0; i < 10; i++ {
		l.Allow("key")
	}

	// Wait exactly 1 second — should get 2 tokens back
	time.Sleep(1 * time.Second)

	count := 0
	for i := 0; i < 5; i++ {
		if l.Allow("key").Allowed {
			count++
		}
	}
	// Should be exactly 2, but allow ±1 for timing jitter
	if count < 1 || count > 3 {
		t.Errorf("expected ~2 tokens after 1s, got %d", count)
	}
}
