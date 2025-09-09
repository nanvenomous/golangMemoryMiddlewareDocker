package middleware

import (
	"context"
	"log"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"
)

type MemoryConfig struct {
	LimitMB      int64
	WarningMB    int64
	MaxQueueSize int
	QueueTimeout time.Duration
	RetryAfter   string
}

type MemoryMiddleware struct {
	config       MemoryConfig
	requestQueue chan *queuedRequest
	mu           sync.RWMutex
	next         http.Handler
}

type queuedRequest struct {
	w       http.ResponseWriter
	r       *http.Request
	handler http.HandlerFunc
	done    chan bool
	queued  time.Time
}

func NewMemoryMiddleware(config *MemoryConfig) *MemoryMiddleware {
	if config == nil {
		config = &MemoryConfig{
			LimitMB:      200,
			WarningMB:    150,
			MaxQueueSize: 100,
			QueueTimeout: 30 * time.Second,
			RetryAfter:   "10",
		}
	}

	// Override with environment variables
	if limit := os.Getenv("MEMORY_LIMIT_MB"); limit != "" {
		if val, err := strconv.ParseInt(limit, 10, 64); err == nil {
			config.LimitMB = val
		}
	}

	if warning := os.Getenv("MEMORY_WARNING_MB"); warning != "" {
		if val, err := strconv.ParseInt(warning, 10, 64); err == nil {
			config.WarningMB = val
		}
	} else if config.WarningMB == 0 || config.WarningMB >= config.LimitMB {
		config.WarningMB = config.LimitMB * 75 / 100
	}

	if queueSize := os.Getenv("MEMORY_QUEUE_SIZE"); queueSize != "" {
		if val, err := strconv.Atoi(queueSize); err == nil {
			config.MaxQueueSize = val
		}
	}

	if timeout := os.Getenv("MEMORY_QUEUE_TIMEOUT_SEC"); timeout != "" {
		if val, err := strconv.Atoi(timeout); err == nil {
			config.QueueTimeout = time.Duration(val) * time.Second
		}
	}

	if retryAfter := os.Getenv("MEMORY_RETRY_AFTER"); retryAfter != "" {
		config.RetryAfter = retryAfter
	}

	mm := &MemoryMiddleware{
		config:       *config,
		requestQueue: make(chan *queuedRequest, config.MaxQueueSize),
	}

	go mm.processRequestQueue()

	log.Printf("🧠 Memory middleware initialized: Warning=%dMB, Limit=%dMB",
		mm.config.WarningMB, mm.config.LimitMB)

	return mm
}

func (mm *MemoryMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	mm.next.ServeHTTP(w, r)
}

func (mm *MemoryMiddleware) Handler(next http.Handler) http.Handler {
	return &MemoryMiddleware{
		config:       mm.config,
		requestQueue: mm.requestQueue,
		mu:           mm.mu,
		next:         next,
	}
}

func (mm *MemoryMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		currentMB := int64(m.Alloc / 1024 / 1024)

		log.Printf("🔍 %s %s - Memory: %dMB/%dMB (%s)",
			r.Method, r.URL.Path, currentMB, mm.config.LimitMB, mm.getMemoryStatus(currentMB))

		if currentMB >= mm.config.LimitMB {
			log.Printf("🚨 MEMORY CRITICAL: %dMB >= %dMB - Rejecting request", currentMB, mm.config.LimitMB)
			w.Header().Set("Retry-After", mm.config.RetryAfter)
			http.Error(w, "Server overloaded, please retry", http.StatusServiceUnavailable)
			return
		}

		if currentMB >= mm.config.WarningMB {
			mm.handleHighMemoryRequest(w, r, func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r)
			}, currentMB)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (mm *MemoryMiddleware) handleHighMemoryRequest(w http.ResponseWriter, r *http.Request, next http.HandlerFunc, currentMB int64) {
	log.Printf("⚠️  MEMORY WARNING: %dMB >= %dMB - Queueing request", currentMB, mm.config.WarningMB)

	select {
	case mm.requestQueue <- &queuedRequest{
		w:       w,
		r:       r,
		handler: next,
		done:    make(chan bool, 1),
		queued:  time.Now(),
	}:
		ctx, cancel := context.WithTimeout(context.Background(), mm.config.QueueTimeout)
		defer cancel()

		select {
		case <-ctx.Done():
			log.Printf("⏰ Request timeout after queuing")
			http.Error(w, "Request timeout", http.StatusRequestTimeout)
		case req := <-mm.requestQueue:
			if time.Since(req.queued) > mm.config.QueueTimeout-5*time.Second {
				log.Printf("⏰ Request expired in queue")
				http.Error(w, "Request expired", http.StatusRequestTimeout)
				return
			}
			req.handler(req.w, req.r)
		}
	default:
		log.Printf("🚫 Queue full - Rejecting request")
		w.Header().Set("Retry-After", "5")
		http.Error(w, "Server busy, queue full", http.StatusServiceUnavailable)
	}
}

func (mm *MemoryMiddleware) processRequestQueue() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		currentMB := int64(m.Alloc / 1024 / 1024)

		if currentMB < mm.config.WarningMB && len(mm.requestQueue) > 0 {
			select {
			case req := <-mm.requestQueue:
				if time.Since(req.queued) > mm.config.QueueTimeout {
					log.Printf("⏰ Dropping expired request from queue")
					continue
				}
				log.Printf("✅ Processing queued request (waited %v)", time.Since(req.queued))
				req.handler(req.w, req.r)
			default:
			}
		}
	}
}

func (mm *MemoryMiddleware) getMemoryStatus(currentMB int64) string {
	if currentMB >= mm.config.LimitMB {
		return "CRITICAL 🚨"
	} else if currentMB >= mm.config.WarningMB {
		return "WARNING ⚠️"
	} else if currentMB >= mm.config.WarningMB/2 {
		return "MODERATE 🟡"
	}
	return "GOOD ✅"
}

func (mm *MemoryMiddleware) GetStats() map[string]interface{} {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return map[string]interface{}{
		"current_mb":   int64(m.Alloc / 1024 / 1024),
		"limit_mb":     mm.config.LimitMB,
		"warning_mb":   mm.config.WarningMB,
		"queue_length": len(mm.requestQueue),
		"queue_cap":    cap(mm.requestQueue),
		"status":       mm.getMemoryStatus(int64(m.Alloc / 1024 / 1024)),
	}
}
