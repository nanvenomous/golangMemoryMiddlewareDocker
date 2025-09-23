package middleware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
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
	config := &RateLimitConfig{
		RequestsPerMinute: 3,
		BurstSize:         3,
	}
	manager := NewRateLimiterManager(config)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	middleware := NewRateLimitMiddleware(manager)(handler)

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
		// Create a fresh manager for this test
		config2 := &RateLimitConfig{
			RequestsPerMinute: 3,
			BurstSize:         3,
		}
		manager2 := NewRateLimiterManager(config2)
		middleware2 := NewRateLimitMiddleware(manager2)(handler)

		// IP 1 makes 3 requests (should all succeed)
		for i := 0; i < 3; i++ {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = "192.168.1.101:12345"
			w := httptest.NewRecorder()

			middleware2.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("IP1 Request %d: expected status %d, got %d", i+1, http.StatusOK, w.Code)
			}
		}

		// IP 2 makes 3 requests (should all succeed)
		for i := 0; i < 3; i++ {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = "192.168.1.102:12345"
			w := httptest.NewRecorder()

			middleware2.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("IP2 Request %d: expected status %d, got %d", i+1, http.StatusOK, w.Code)
			}
		}

		// IP 1 makes another request (should be blocked)
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "192.168.1.101:12345"
		w := httptest.NewRecorder()

		middleware2.ServeHTTP(w, req)

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

	config := NewRateLimitConfigFromEnv()

	if config.RequestsPerMinute != 5 {
		t.Errorf("Expected RequestsPerMinute to be 5, got %d", config.RequestsPerMinute)
	}

	// Test with invalid value (should use default)
	os.Setenv("RATE_LIMIT_REQUESTS_PER_MINUTE", "invalid")
	config = NewRateLimitConfigFromEnv()

	if config.RequestsPerMinute != 60 {
		t.Errorf("Expected RequestsPerMinute to be 60 (default), got %d", config.RequestsPerMinute)
	}
}

func TestNewRateLimitConfig(t *testing.T) {
	config := NewRateLimitConfig(120)

	if config.RequestsPerMinute != 120 {
		t.Errorf("Expected RequestsPerMinute to be 120, got %d", config.RequestsPerMinute)
	}

	expectedBurst := 12 // 120 / 10
	if config.BurstSize != expectedBurst {
		t.Errorf("Expected BurstSize to be %d, got %d", expectedBurst, config.BurstSize)
	}

	// Test with small value (burst should be minimum 1)
	config2 := NewRateLimitConfig(5)
	if config2.BurstSize != 1 {
		t.Errorf("Expected BurstSize to be 1 for small rate, got %d", config2.BurstSize)
	}
}

func TestRateLimiterManager(t *testing.T) {
	config := &RateLimitConfig{
		RequestsPerMinute: 60,
		BurstSize:         6,
	}
	manager := NewRateLimiterManager(config)

	testIP := "192.168.1.200"

	// First call should create a new limiter
	limiter1 := manager.getRateLimiter(testIP)
	if limiter1 == nil {
		t.Error("Expected limiter to be created")
	}

	// Second call should return the same limiter
	limiter2 := manager.getRateLimiter(testIP)
	if limiter1 != limiter2 {
		t.Error("Expected same limiter instance")
	}

	// Different IP should get different limiter
	limiter3 := manager.getRateLimiter("192.168.1.201")
	if limiter1 == limiter3 {
		t.Error("Expected different limiter for different IP")
	}

	// Test that limiter works correctly
	// Should allow burst size requests immediately
	for i := 0; i < config.BurstSize; i++ {
		if !limiter1.Allow() {
			t.Errorf("Request %d should be allowed (within burst)", i+1)
		}
	}

	// Next request should be denied
	if limiter1.Allow() {
		t.Error("Request beyond burst should be denied")
	}
}

func BenchmarkRateLimiterManager(b *testing.B) {
	config := &RateLimitConfig{
		RequestsPerMinute: 1000,
		BurstSize:         100,
	}
	manager := NewRateLimiterManager(config)

	testIP := "192.168.1.100"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		limiter := manager.getRateLimiter(testIP)
		limiter.Allow()
	}
}
