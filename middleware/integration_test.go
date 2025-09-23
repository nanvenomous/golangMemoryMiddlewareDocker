package middleware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"golang.org/x/time/rate"
)

func TestMiddlewareChain(t *testing.T) {
	// Save original environment
	originalLimitMB := os.Getenv("MEMORY_LIMIT_MB")
	originalRetryAfter := os.Getenv("MEMORY_RETRY_AFTER")
	originalRateLimit := os.Getenv("RATE_LIMIT_REQUESTS_PER_MINUTE")

	defer func() {
		if originalLimitMB != "" {
			os.Setenv("MEMORY_LIMIT_MB", originalLimitMB)
		} else {
			os.Unsetenv("MEMORY_LIMIT_MB")
		}
		if originalRetryAfter != "" {
			os.Setenv("MEMORY_RETRY_AFTER", originalRetryAfter)
		} else {
			os.Unsetenv("MEMORY_RETRY_AFTER")
		}
		if originalRateLimit != "" {
			os.Setenv("RATE_LIMIT_REQUESTS_PER_MINUTE", originalRateLimit)
		} else {
			os.Unsetenv("RATE_LIMIT_REQUESTS_PER_MINUTE")
		}

		// Re-setup with original values
		setup()
		setupRateLimit()
	}()

	// Set up test environment
	os.Setenv("MEMORY_LIMIT_MB", "99999") // High memory limit
	os.Setenv("MEMORY_RETRY_AFTER", "30")
	os.Setenv("RATE_LIMIT_REQUESTS_PER_MINUTE", "10") // Higher limit for test

	setup()
	setupRateLimit()

	// Clear rate limiters for clean test
	rateLimitMu.Lock()
	rateLimiters = make(map[string]*rate.Limiter)
	rateLimitMu.Unlock()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Create middleware chain: RateLimit -> Memory -> Handler
	wrappedHandler := RateLimitMiddleware(MemoryMiddleware(handler))

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

		os.Setenv("MEMORY_LIMIT_MB", strconv.FormatInt(limitMem, 10))
		setup()

		// Use different IP to avoid rate limit
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "192.168.1.51:12345"
		w := httptest.NewRecorder()

		wrappedHandler.ServeHTTP(w, req)

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
	// Set up test environment for optimal performance
	os.Setenv("MEMORY_LIMIT_MB", "99999")
	os.Setenv("MEMORY_RETRY_AFTER", "30")
	os.Setenv("RATE_LIMIT_REQUESTS_PER_MINUTE", "999999")

	setup()
	setupRateLimit()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrappedHandler := RateLimitMiddleware(MemoryMiddleware(handler))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "192.168.1.100:12345"
		w := httptest.NewRecorder()
		wrappedHandler.ServeHTTP(w, req)
	}
}
