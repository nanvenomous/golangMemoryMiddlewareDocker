package middleware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"golang.org/x/time/rate"
)

func TestGetRealIP(t *testing.T) {
	tests := []struct {
		name       string
		headers    map[string]string
		remoteAddr string
		expectedIP string
	}{
		{
			name: "X-Forwarded-For header",
			headers: map[string]string{
				"X-Forwarded-For": "192.168.1.1",
			},
			remoteAddr: "10.0.0.1:12345",
			expectedIP: "192.168.1.1",
		},
		{
			name: "X-Real-IP header",
			headers: map[string]string{
				"X-Real-IP": "192.168.1.2",
			},
			remoteAddr: "10.0.0.1:12345",
			expectedIP: "192.168.1.2",
		},
		{
			name:       "Remote address fallback",
			headers:    map[string]string{},
			remoteAddr: "192.168.1.3:12345",
			expectedIP: "192.168.1.3",
		},
		{
			name: "X-Forwarded-For takes precedence",
			headers: map[string]string{
				"X-Forwarded-For": "192.168.1.4",
				"X-Real-IP":       "192.168.1.5",
			},
			remoteAddr: "10.0.0.1:12345",
			expectedIP: "192.168.1.4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.remoteAddr

			for key, value := range tt.headers {
				req.Header.Set(key, value)
			}

			ip := getRealIP(req)
			if ip != tt.expectedIP {
				t.Errorf("getRealIP() = %v, want %v", ip, tt.expectedIP)
			}
		})
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	// Save original config and restore after test
	originalConfig := rlConfig
	defer func() {
		rlConfig = originalConfig
	}()

	// Set test configuration
	rlConfig = RateLimitConfig{
		RequestsPerMinute: 3,
		BurstSize:         3,
	}

	// Clear rate limiters for clean test
	rateLimitMu.Lock()
	rateLimiters = make(map[string]*rate.Limiter)
	rateLimitMu.Unlock()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	middleware := RateLimitMiddleware(handler)

	t.Run("allows requests under limit", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = "192.168.1.100:12345"
			w := httptest.NewRecorder()

			middleware.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("Request %d: expected status %d, got %d", i+1, http.StatusOK, w.Code)
			}
		}
	})

	t.Run("blocks requests over limit", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "192.168.1.100:12345"
		w := httptest.NewRecorder()

		middleware.ServeHTTP(w, req)

		if w.Code != http.StatusTooManyRequests {
			t.Errorf("Expected status %d, got %d", http.StatusTooManyRequests, w.Code)
		}

		// Check headers
		if w.Header().Get("X-RateLimit-Limit") != "3" {
			t.Errorf("Expected X-RateLimit-Limit header to be '3', got '%s'", w.Header().Get("X-RateLimit-Limit"))
		}

		if w.Header().Get("Retry-After") != "60" {
			t.Errorf("Expected Retry-After header to be '60', got '%s'", w.Header().Get("Retry-After"))
		}
	})

	t.Run("different IPs have separate limits", func(t *testing.T) {
		// Clear rate limiters for clean test
		rateLimitMu.Lock()
		rateLimiters = make(map[string]*rate.Limiter)
		rateLimitMu.Unlock()

		// IP 1 makes 3 requests (should all succeed)
		for i := 0; i < 3; i++ {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = "192.168.1.101:12345"
			w := httptest.NewRecorder()

			middleware.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("IP1 Request %d: expected status %d, got %d", i+1, http.StatusOK, w.Code)
			}
		}

		// IP 2 makes 3 requests (should all succeed)
		for i := 0; i < 3; i++ {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = "192.168.1.102:12345"
			w := httptest.NewRecorder()

			middleware.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("IP2 Request %d: expected status %d, got %d", i+1, http.StatusOK, w.Code)
			}
		}

		// IP 1 makes another request (should be blocked)
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "192.168.1.101:12345"
		w := httptest.NewRecorder()

		middleware.ServeHTTP(w, req)

		if w.Code != http.StatusTooManyRequests {
			t.Errorf("IP1 overflow request: expected status %d, got %d", http.StatusTooManyRequests, w.Code)
		}
	})
}

func TestRateLimitWithEnvironmentVariables(t *testing.T) {
	// Save original environment
	originalEnv := os.Getenv("RATE_LIMIT_REQUESTS_PER_MINUTE")
	defer func() {
		if originalEnv != "" {
			os.Setenv("RATE_LIMIT_REQUESTS_PER_MINUTE", originalEnv)
		} else {
			os.Unsetenv("RATE_LIMIT_REQUESTS_PER_MINUTE")
		}
	}()

	// Test with custom rate limit
	os.Setenv("RATE_LIMIT_REQUESTS_PER_MINUTE", "5")

	// Re-setup rate limiting with new environment
	setupRateLimit()

	if rlConfig.RequestsPerMinute != 5 {
		t.Errorf("Expected RequestsPerMinute to be 5, got %d", rlConfig.RequestsPerMinute)
	}

	// Test with invalid value (should use default)
	os.Setenv("RATE_LIMIT_REQUESTS_PER_MINUTE", "invalid")
	setupRateLimit()

	if rlConfig.RequestsPerMinute != 60 {
		t.Errorf("Expected RequestsPerMinute to be 60 (default), got %d", rlConfig.RequestsPerMinute)
	}
}

func TestGetRateLimiter(t *testing.T) {
	// Save original config and restore after test
	originalConfig := rlConfig
	defer func() {
		rlConfig = originalConfig
	}()

	// Set test configuration
	rlConfig = RateLimitConfig{
		RequestsPerMinute: 60,
		BurstSize:         6,
	}

	// Clear rate limiters for clean test
	rateLimitMu.Lock()
	rateLimiters = make(map[string]*rate.Limiter)
	rateLimitMu.Unlock()

	testIP := "192.168.1.200"

	// First call should create a new limiter
	limiter1 := getRateLimiter(testIP)
	if limiter1 == nil {
		t.Error("Expected limiter to be created")
	}

	// Second call should return the same limiter
	limiter2 := getRateLimiter(testIP)
	if limiter1 != limiter2 {
		t.Error("Expected same limiter instance")
	}

	// Different IP should get different limiter
	limiter3 := getRateLimiter("192.168.1.201")
	if limiter1 == limiter3 {
		t.Error("Expected different limiter for different IP")
	}

	// Test that limiter works correctly
	// Should allow burst size requests immediately
	for i := 0; i < rlConfig.BurstSize; i++ {
		if !limiter1.Allow() {
			t.Errorf("Request %d should be allowed (within burst)", i+1)
		}
	}

	// Next request should be denied
	if limiter1.Allow() {
		t.Error("Request beyond burst should be denied")
	}
}

func BenchmarkGetRateLimiter(b *testing.B) {
	// Set test configuration
	rlConfig = RateLimitConfig{
		RequestsPerMinute: 1000,
		BurstSize:         100,
	}

	// Clear rate limiters for clean test
	rateLimitMu.Lock()
	rateLimiters = make(map[string]*rate.Limiter)
	rateLimitMu.Unlock()

	testIP := "192.168.1.100"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		limiter := getRateLimiter(testIP)
		limiter.Allow()
	}
}
