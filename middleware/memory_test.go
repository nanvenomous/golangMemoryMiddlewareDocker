package middleware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestMemoryMiddleware(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	t.Run("allows requests under memory limit", func(t *testing.T) {
		// Create config with high memory limit
		config := NewMemoryConfig(99999, "30")
		middleware := NewMemoryMiddleware(config)(handler)

		req := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		middleware.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
		}

		body := w.Body.String()
		if body != "OK" {
			t.Errorf("Expected body 'OK', got '%s'", body)
		}
	})

	t.Run("blocks requests over memory limit", func(t *testing.T) {
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

		config := NewMemoryConfig(limitMem, "60")
		middleware := NewMemoryMiddleware(config)(handler)

		req := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		middleware.ServeHTTP(w, req)

		// Keep reference to prevent GC
		_ = memoryHog

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("Expected status %d, got %d", http.StatusServiceUnavailable, w.Code)
		}

		// Check Retry-After header
		retryAfter := w.Header().Get("Retry-After")
		if retryAfter != "60" {
			t.Errorf("Expected Retry-After header to be '60', got '%s'", retryAfter)
		}

		// Check error message
		body := w.Body.String()
		expectedBody := "Server overloaded, please retry\n"
		if body != expectedBody {
			t.Errorf("Expected body '%s', got '%s'", expectedBody, body)
		}
	})
}

func TestNewMemoryConfigFromEnv(t *testing.T) {
	// Save original environment
	originalLimitMB := os.Getenv("MEMORY_LIMIT_MB")
	originalRetryAfter := os.Getenv("MEMORY_RETRY_AFTER")

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
	}()

	t.Run("valid configuration", func(t *testing.T) {
		os.Setenv("MEMORY_LIMIT_MB", "100")
		os.Setenv("MEMORY_RETRY_AFTER", "30")

		config, err := NewMemoryConfigFromEnv()
		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}

		if config.LimitMB != 100 {
			t.Errorf("Expected LimitMB to be 100, got %d", config.LimitMB)
		}

		if config.RetryAfter != "30" {
			t.Errorf("Expected RetryAfter to be '30', got '%s'", config.RetryAfter)
		}
	})

	t.Run("missing MEMORY_LIMIT_MB", func(t *testing.T) {
		os.Unsetenv("MEMORY_LIMIT_MB")
		os.Setenv("MEMORY_RETRY_AFTER", "30")

		_, err := NewMemoryConfigFromEnv()
		if err == nil {
			t.Error("Expected error for missing MEMORY_LIMIT_MB")
		}
	})

	t.Run("missing MEMORY_RETRY_AFTER", func(t *testing.T) {
		os.Setenv("MEMORY_LIMIT_MB", "100")
		os.Unsetenv("MEMORY_RETRY_AFTER")

		_, err := NewMemoryConfigFromEnv()
		if err == nil {
			t.Error("Expected error for missing MEMORY_RETRY_AFTER")
		}
	})

	t.Run("invalid MEMORY_LIMIT_MB", func(t *testing.T) {
		os.Setenv("MEMORY_LIMIT_MB", "invalid")
		os.Setenv("MEMORY_RETRY_AFTER", "30")

		_, err := NewMemoryConfigFromEnv()
		if err == nil {
			t.Error("Expected error for invalid MEMORY_LIMIT_MB")
		}
	})
}

func TestNewMemoryConfig(t *testing.T) {
	config := NewMemoryConfig(200, "45")

	if config.LimitMB != 200 {
		t.Errorf("Expected LimitMB to be 200, got %d", config.LimitMB)
	}

	if config.RetryAfter != "45" {
		t.Errorf("Expected RetryAfter to be '45', got '%s'", config.RetryAfter)
	}
}

func TestCurrentMB(t *testing.T) {
	mb := currentMB()

	if mb < 0 {
		t.Errorf("Expected currentMB to be non-negative, got %d", mb)
	}

	// Memory usage should be reasonable for a test program
	if mb > 1000 {
		t.Errorf("Expected currentMB to be reasonable (<1000MB), got %d", mb)
	}
}

func BenchmarkMemoryMiddleware(b *testing.B) {
	config := NewMemoryConfig(99999, "30")
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := NewMemoryMiddleware(config)(handler)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		middleware.ServeHTTP(w, req)
	}
}

func BenchmarkCurrentMB(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		currentMB()
	}
}
