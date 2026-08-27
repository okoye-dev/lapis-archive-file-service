package handlers

import (
	"testing"
	"time"
)

func TestRateLimiterBasic(t *testing.T) {
	rl := newRateLimiter(2, time.Minute)
	if !rl.Allow("a") || !rl.Allow("a") {
		t.Fatal("first two requests should pass")
	}
	if rl.Allow("a") {
		t.Error("third request should be blocked")
	}
	if !rl.Allow("b") {
		t.Error("a different key should still pass")
	}
}

func TestRateLimiterWindowRecovery(t *testing.T) {
	rl := newRateLimiter(1, time.Minute)
	if !rl.Allow("a") {
		t.Fatal("first should pass")
	}
	if rl.Allow("a") {
		t.Fatal("second should be blocked")
	}
	// Age the recorded event past the window; the next call prunes it.
	rl.events["a"] = []time.Time{time.Now().Add(-2 * time.Minute)}
	if !rl.Allow("a") {
		t.Error("should pass again once the window has elapsed")
	}
}

func TestRateLimiterEvictsStale(t *testing.T) {
	rl := newRateLimiter(5, time.Minute)
	rl.maxKeys = 2
	// Two stale keys fill the table.
	rl.events["old1"] = []time.Time{time.Now().Add(-2 * time.Minute)}
	rl.events["old2"] = []time.Time{time.Now().Add(-2 * time.Minute)}
	if !rl.Allow("new") {
		t.Error("a new key should be admitted after stale keys are evicted")
	}

	// With the table full of fresh keys, a new key can't be admitted.
	rl2 := newRateLimiter(5, time.Minute)
	rl2.maxKeys = 2
	rl2.Allow("f1")
	rl2.Allow("f2")
	if rl2.Allow("f3") {
		t.Error("a new key should be refused when the table is full of fresh keys")
	}
}
