package middleware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
)

func TestMemoryMiddleware(t *testing.T) {
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

		// Re-setup with original values
		setup()
	}()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	t.Run("allows requests under memory limit", func(t *testing.T) {
		// Set a very high memory limit for this test
		os.Setenv("MEMORY_LIMIT_MB", "99999")
		os.Setenv("MEMORY_RETRY_AFTER", "30")

		err := setup()
		if err != nil {
			t.Fatalf("Setup failed: %v", err)
		}

		middleware := MemoryMiddleware(handler)

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

		os.Setenv("MEMORY_LIMIT_MB", strconv.FormatInt(limitMem, 10))
		os.Setenv("MEMORY_RETRY_AFTER", "60")

		err := setup()
		if err != nil {
			t.Fatalf("Setup failed: %v", err)
		}

		middleware := MemoryMiddleware(handler)

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

func TestSetup(t *testing.T) {
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

		err := setup()
		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}

		if limitMB != 100 {
			t.Errorf("Expected limitMB to be 100, got %d", limitMB)
		}

		if retryAfter != "30" {
			t.Errorf("Expected retryAfter to be '30', got '%s'", retryAfter)
		}
	})

	t.Run("missing MEMORY_LIMIT_MB", func(t *testing.T) {
		os.Unsetenv("MEMORY_LIMIT_MB")
		os.Setenv("MEMORY_RETRY_AFTER", "30")

		err := setup()
		if err == nil {
			t.Error("Expected error for missing MEMORY_LIMIT_MB")
		}
	})

	t.Run("missing MEMORY_RETRY_AFTER", func(t *testing.T) {
		os.Setenv("MEMORY_LIMIT_MB", "100")
		os.Unsetenv("MEMORY_RETRY_AFTER")

		err := setup()
		if err == nil {
			t.Error("Expected error for missing MEMORY_RETRY_AFTER")
		}
	})

	t.Run("invalid MEMORY_LIMIT_MB", func(t *testing.T) {
		os.Setenv("MEMORY_LIMIT_MB", "invalid")
		os.Setenv("MEMORY_RETRY_AFTER", "30")

		err := setup()
		if err == nil {
			t.Error("Expected error for invalid MEMORY_LIMIT_MB")
		}
	})
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
	// Set environment for benchmark
	os.Setenv("MEMORY_LIMIT_MB", "99999")
	os.Setenv("MEMORY_RETRY_AFTER", "30")
	setup()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	middleware := MemoryMiddleware(handler)

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
