package main

import (
	"fmt"
	"log"
	"net/http"
	"runtime"
	"strconv"
	"sync"
)

var (
	memoryHog [][]byte
	mu        sync.Mutex
)

func main() {
	http.HandleFunc("/", healthHandler)
	http.HandleFunc("/allocate", allocateHandler)
	http.HandleFunc("/status", statusHandler)

	fmt.Println("Starting server on :8080")
	fmt.Println("Endpoints:")
	fmt.Println("  GET  /        - Health check")
	fmt.Println("  POST /allocate?mb=N - Allocate N MB of memory")
	fmt.Println("  GET  /status  - Memory status")

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

	// log.Printf("Allocating %s \n", mbParam)

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

	fmt.Fprintf(w, "Memory Status:\n")
	fmt.Fprintf(w, "Allocated: %d MB\n", m.Alloc/1024/1024)
	fmt.Fprintf(w, "Total Allocated: %d MB\n", m.TotalAlloc/1024/1024)
	fmt.Fprintf(w, "System Memory: %d MB\n", m.Sys/1024/1024)
	fmt.Fprintf(w, "Memory Chunks: %d\n", chunks)
}
