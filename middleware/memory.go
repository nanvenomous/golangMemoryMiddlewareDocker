package middleware

import (
	"errors"
	"log"
	"net/http"
	"os"
	"runtime"
	"strconv"
)

type MemoryConfig struct {
	LimitMB    int64
	RetryAfter string
}

func NewMemoryConfigFromEnv() (*MemoryConfig, error) {
	limitStr := os.Getenv("MEMORY_LIMIT_MB")
	retryAfter := os.Getenv("MEMORY_RETRY_AFTER")

	if limitStr == "" || retryAfter == "" {
		return nil, errors.New("Must set environment variables MEMORY_LIMIT_MB, MEMORY_RETRY_AFTER")
	}

	limitMB, err := strconv.ParseInt(limitStr, 10, 64)
	if err != nil {
		return nil, err
	}

	log.Printf("MEMORY_LIMIT_MB: %s, MEMORY_RETRY_AFTER: %s", limitStr, retryAfter)

	return &MemoryConfig{
		LimitMB:    limitMB,
		RetryAfter: retryAfter,
	}, nil
}

func NewMemoryConfig(limitMB int64, retryAfter string) *MemoryConfig {
	return &MemoryConfig{
		LimitMB:    limitMB,
		RetryAfter: retryAfter,
	}
}

func currentMB() int64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int64(m.Alloc / 1024 / 1024)
}

func NewMemoryMiddleware(config *MemoryConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			memMB := currentMB()
			if memMB >= config.LimitMB {
				log.Printf("🚨 MEMORY CRITICAL: %dMB >= %dMB - Rejecting request", memMB, config.LimitMB)
				w.Header().Set("Retry-After", config.RetryAfter)
				http.Error(w, "Server overloaded, please retry", http.StatusServiceUnavailable)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// MemoryMiddleware creates a memory middleware using environment variables (for backward compatibility)
func MemoryMiddleware(next http.Handler) http.Handler {
	config, err := NewMemoryConfigFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	return NewMemoryMiddleware(config)(next)
}
