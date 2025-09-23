package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiddlewareChain(t *testing.T) {
	// Create configurations directly
	memoryConfig := NewMemoryConfig(99999, "30") // High memory limit
	rateLimitConfig := &RateLimitConfig{
		RequestsPerMinute: 10,
		BurstSize:         1,
	}

	manager := NewRateLimiterManager(rateLimitConfig)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Create middleware chain: RateLimit -> Memory -> Handler
	wrappedHandler := NewRateLimitMiddleware(manager)(NewMemoryMiddleware(memoryConfig)(handler))

	t.Run("allows requests under both limits", func(t *testing.T) {
		// With 10 requests per minute, burst size should be 1, so we can make 1 request immediately
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "192.168.1.50:12345"
		w := httptest.NewRecorder()

		wrappedHandler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Request 1: expected status %d, got %d", http.StatusOK, w.Code)
		}
	})

	t.Run("rate limit blocks before memory check", func(t *testing.T) {
		// Second request from same IP should be blocked by rate limit
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "192.168.1.50:12345"
		w := httptest.NewRecorder()

		wrappedHandler.ServeHTTP(w, req)

		if w.Code != http.StatusTooManyRequests {
			t.Errorf("Expected status %d (rate limit), got %d", http.StatusTooManyRequests, w.Code)
		}

		// Check it's the rate limit error, not memory
		if w.Header().Get("X-RateLimit-Limit") == "" {
			t.Error("Expected rate limit headers to be present")
		}
	})

	t.Run("memory limit blocks when rate limit allows", func(t *testing.T) {
		// Allocate some memory to increase usage
		memoryHog := make([][]byte, 10)
		for i := 0; i < 10; i++ {
			memoryHog[i] = make([]byte, 1024*1024) // 1MB each
		}

		// Get current memory usage and set limit just below it
		currentMem := currentMB()
		limitMem := currentMem - 1
		if limitMem < 1 {
			limitMem = 1
		}

		// Create new configs with low memory limit
		lowMemoryConfig := NewMemoryConfig(limitMem, "30")
		rateLimitConfig2 := &RateLimitConfig{
			RequestsPerMinute: 10,
			BurstSize:         1,
		}
		manager2 := NewRateLimiterManager(rateLimitConfig2)

		// Create new handler chain
		testHandler := NewRateLimitMiddleware(manager2)(NewMemoryMiddleware(lowMemoryConfig)(handler))

		// Use different IP to avoid rate limit
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "192.168.1.51:12345"
		w := httptest.NewRecorder()

		testHandler.ServeHTTP(w, req)

		// Keep reference to prevent GC
		_ = memoryHog

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("Expected status %d (memory limit), got %d", http.StatusServiceUnavailable, w.Code)
		}

		// Check it's the memory error, not rate limit
		if w.Header().Get("Retry-After") != "30" {
			t.Error("Expected memory limit Retry-After header")
		}
		if w.Header().Get("X-RateLimit-Limit") != "" {
			t.Error("Should not have rate limit headers")
		}
	})
}

func TestMiddlewareOrder(t *testing.T) {
	// This test verifies that middleware is applied in the correct order
	// Rate limiting should happen first, then memory checking

	var callOrder []string

	rateLimitHandler := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callOrder = append(callOrder, "ratelimit")
			next.ServeHTTP(w, r)
		})
	}

	memoryHandler := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callOrder = append(callOrder, "memory")
			next.ServeHTTP(w, r)
		})
	}

	baseHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callOrder = append(callOrder, "handler")
		w.WriteHeader(http.StatusOK)
	})

	// Create chain: rateLimitHandler -> memoryHandler -> baseHandler
	wrappedHandler := rateLimitHandler(memoryHandler(baseHandler))

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	callOrder = []string{} // Reset
	wrappedHandler.ServeHTTP(w, req)

	expectedOrder := []string{"ratelimit", "memory", "handler"}
	if len(callOrder) != len(expectedOrder) {
		t.Fatalf("Expected %d calls, got %d: %v", len(expectedOrder), len(callOrder), callOrder)
	}

	for i, expected := range expectedOrder {
		if callOrder[i] != expected {
			t.Errorf("Call %d: expected '%s', got '%s'", i, expected, callOrder[i])
		}
	}
}

func BenchmarkMiddlewareChain(b *testing.B) {
	// Create high-performance configs
	memoryConfig := NewMemoryConfig(99999, "30")
	rateLimitConfig := &RateLimitConfig{
		RequestsPerMinute: 999999,
		BurstSize:         99999,
	}
	manager := NewRateLimiterManager(rateLimitConfig)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrappedHandler := NewRateLimitMiddleware(manager)(NewMemoryMiddleware(memoryConfig)(handler))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "192.168.1.100:12345"
		w := httptest.NewRecorder()
		wrappedHandler.ServeHTTP(w, req)
	}
}
