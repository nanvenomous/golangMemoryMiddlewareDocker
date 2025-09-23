package middleware

import (
	"errors"
	"log"
	"net/http"
	"os"
	"runtime"
	"strconv"
)

var (
	limitMB    int64
	retryAfter = os.Getenv("MEMORY_RETRY_AFTER")
)

func setup() error {
	var (
		err      error
		limitStr = os.Getenv("MEMORY_LIMIT_MB")
	)

	retryAfter = os.Getenv("MEMORY_RETRY_AFTER")

	if limitStr == "" || retryAfter == "" {
		return errors.New("Must set environment variables MEMORY_LIMIT_MB, MEMORY_RETRY_AFTER")
	}

	limitMB, err = strconv.ParseInt(limitStr, 10, 64)
	if err != nil {
		return err
	}

	log.Println("MEMORY_LIMIT_MB: ", limitStr, ", ", "MEMORY_RETRY_AFTER: ", retryAfter)

	return nil
}

func currentMB() int64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int64(m.Alloc / 1024 / 1024)
}

func MemoryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		memMB := currentMB()
		if memMB >= limitMB {
			log.Printf("🚨 MEMORY CRITICAL: %dMB >= %dMB - Rejecting request", memMB, limitMB)
			w.Header().Set("Retry-After", retryAfter)
			http.Error(w, "Server overloaded, please retry", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func init() {
	err := setup()
	if err != nil {
		log.Fatal(err)
	}
}
