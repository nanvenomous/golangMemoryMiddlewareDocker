package main

import (
	"fmt"
	"log"
	"net/http"
	"runtime"
	"strconv"
	"sync"

	"memory/middleware"
)

var (
	memoryHog [][]byte
	mu        sync.Mutex
	memMW     *middleware.MemoryMiddleware
)

func main() {
	// Initialize memory middleware with default config (env vars will override)
	memMW = middleware.NewMemoryMiddleware(nil)

	// Wrap handlers with memory middleware
	http.HandleFunc("/", memMW.Handler(healthHandler))
	http.HandleFunc("/allocate", memMW.Handler(allocateHandler))
	http.HandleFunc("/status", memMW.Handler(statusHandler))

	log.Printf("🚀 Starting server on :8080")
	log.Printf("📡 Endpoints:")
	log.Printf("   GET  /        - Health check")
	log.Printf("   POST /allocate?mb=N - Allocate N MB of memory")
	log.Printf("   GET  /status  - Memory status")

	log.Fatal(http.ListenAndServe(":8080", nil))
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Server is running\n")
}

func allocateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	mbParam := r.URL.Query().Get("mb")
	if mbParam == "" {
		mbParam = "10"
	}

	mb, err := strconv.Atoi(mbParam)
	if err != nil || mb <= 0 {
		http.Error(w, "Invalid mb parameter", http.StatusBadRequest)
		return
	}

	mu.Lock()
	defer mu.Unlock()

	bytes := mb * 1024 * 1024
	chunk := make([]byte, bytes)
	for i := range chunk {
		chunk[i] = byte(i % 256)
	}

	memoryHog = append(memoryHog, chunk)

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	log.Printf("Allocated %d MB. Total allocated: %d MB\n", mb, m.Alloc/1024/1024)
	fmt.Fprintf(w, "Allocated %d MB. Total allocated: %d MB\n", mb, m.Alloc/1024/1024)
}

func statusHandler(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	mu.Lock()
	chunks := len(memoryHog)
	mu.Unlock()

	stats := memMW.GetStats()

	fmt.Fprintf(w, "🖥️  Memory Status:\n")
	fmt.Fprintf(w, "Allocated: %d MB (%s)\n", stats["current_mb"], stats["status"])
	fmt.Fprintf(w, "Total Allocated: %d MB\n", m.TotalAlloc/1024/1024)
	fmt.Fprintf(w, "System Memory: %d MB\n", m.Sys/1024/1024)
	fmt.Fprintf(w, "Memory Chunks: %d\n", chunks)
	fmt.Fprintf(w, "Queue Length: %d\n", stats["queue_length"])
	fmt.Fprintf(w, "Limits: Warning=%dMB, Max=%dMB\n", stats["warning_mb"], stats["limit_mb"])
}
