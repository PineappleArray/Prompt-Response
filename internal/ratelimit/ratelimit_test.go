package ratelimit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"prompt-response/internal/auth"
)

func TestBucket_AllowWithinBurst(t *testing.T) {
	now := time.Now()
	b := newBucket(3, 1.0, func() time.Time { return now })

	for i := 0; i < 3; i++ {
		ok, _ := b.Allow()
		if !ok {
			t.Errorf("Allow() should succeed on attempt %d", i+1)
		}
	}

	ok, remaining := b.Allow()
	if ok {
		t.Error("Allow() should fail after burst exhausted")
	}
	if remaining != 0 {
		t.Errorf("remaining should be 0, got %f", remaining)
	}
}

func TestBucket_Refill(t *testing.T) {
	mu := sync.Mutex{}
	now := time.Now()
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}

	// 60 RPM = 1 token/sec, burst of 2
	b := newBucket(2, 1.0, clock)

	// Drain all tokens
	b.Allow()
	b.Allow()
	ok, _ := b.Allow()
	if ok {
		t.Fatal("bucket should be empty")
	}

	// Advance 1 second — should refill 1 token
	mu.Lock()
	now = now.Add(1 * time.Second)
	mu.Unlock()

	ok, remaining := b.Allow()
	if !ok {
		t.Error("Allow() should succeed after refill")
	}
	if remaining < 0 {
		t.Errorf("remaining should be non-negative, got %f", remaining)
	}
}

func TestBucket_RefillCappedAtCapacity(t *testing.T) {
	mu := sync.Mutex{}
	now := time.Now()
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}

	// Burst of 3, rate 1/sec
	b := newBucket(3, 1.0, clock)

	// Use 1 token
	b.Allow()

	// Wait 10 seconds — should refill to capacity (3), not 12
	mu.Lock()
	now = now.Add(10 * time.Second)
	mu.Unlock()

	// Should allow exactly 3 (capacity), not more
	for i := 0; i < 3; i++ {
		ok, _ := b.Allow()
		if !ok {
			t.Errorf("Allow() should succeed on attempt %d after refill", i+1)
		}
	}
	ok, _ := b.Allow()
	if ok {
		t.Error("Allow() should fail — tokens capped at capacity")
	}
}

func TestRegistry_PerKeyIsolation(t *testing.T) {
	reg := NewRegistry(Config{RequestsPerMinute: 60, Burst: 2})

	// Drain key "a"
	reg.Allow("a")
	reg.Allow("a")
	ok, _ := reg.Allow("a")
	if ok {
		t.Error("key 'a' should be exhausted")
	}

	// Key "b" should still have tokens
	ok, _ = reg.Allow("b")
	if !ok {
		t.Error("key 'b' should still have tokens")
	}
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	reg := NewRegistry(Config{RequestsPerMinute: 6000, Burst: 100})

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				reg.Allow(key)
			}
		}(string(rune('a' + i)))
	}
	wg.Wait()
}

func TestRegistry_Limit(t *testing.T) {
	reg := NewRegistry(Config{RequestsPerMinute: 120, Burst: 5})
	if got := reg.Limit(); got != 120 {
		t.Errorf("Limit() = %f, want 120", got)
	}
}

func TestMiddleware_AllowsWithinLimit(t *testing.T) {
	reg := NewRegistry(Config{RequestsPerMinute: 60, Burst: 5})

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := Middleware(reg)(inner)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !called {
		t.Error("inner handler was not called")
	}
	if w.Header().Get("X-RateLimit-Limit") != "60" {
		t.Errorf("expected X-RateLimit-Limit=60, got %s", w.Header().Get("X-RateLimit-Limit"))
	}
}

func TestMiddleware_RejectsOverLimit(t *testing.T) {
	now := time.Now()
	reg := newRegistryForTest(Config{RequestsPerMinute: 60, Burst: 1}, func() time.Time { return now })

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := Middleware(reg)(inner)

	// First request succeeds
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("first request should succeed, got %d", w.Code)
	}

	// Second request exceeds burst
	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header")
	}

	var body rateLimitErrorResponse
	json.Unmarshal(w.Body.Bytes(), &body)
	if body.Error.Code != "rate_limit_exceeded" {
		t.Errorf("expected code rate_limit_exceeded, got %s", body.Error.Code)
	}
}

func TestMiddleware_SkipsHealthz(t *testing.T) {
	now := time.Now()
	reg := newRegistryForTest(Config{RequestsPerMinute: 60, Burst: 0}, func() time.Time { return now })

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := Middleware(reg)(inner)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !called {
		t.Error("inner handler was not called for /healthz")
	}
}

func TestMiddleware_UsesTenantFromAuth(t *testing.T) {
	reg := NewRegistry(Config{RequestsPerMinute: 60, Burst: 2})

	var lastKey string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := Middleware(reg)(inner)

	// Simulate authenticated request
	ctx := auth.ContextWithTenant(context.Background(), auth.Tenant{ID: "acme"})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	_ = lastKey
}

func TestClientKey_WithTenant(t *testing.T) {
	ctx := auth.ContextWithTenant(context.Background(), auth.Tenant{ID: "acme"})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = req.WithContext(ctx)

	got := clientKey(req)
	if got != "acme" {
		t.Errorf("clientKey() = %q, want %q", got, "acme")
	}
}

func TestClientKey_FallbackToIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"

	got := clientKey(req)
	if got != "10.0.0.1" {
		t.Errorf("clientKey() = %q, want %q", got, "10.0.0.1")
	}
}

func TestClientKey_NoPort(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1"

	got := clientKey(req)
	if got != "10.0.0.1" {
		t.Errorf("clientKey() = %q, want %q", got, "10.0.0.1")
	}
}
