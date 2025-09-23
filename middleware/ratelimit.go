package middleware

import (
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type RateLimitConfig struct {
	RequestsPerMinute int
	BurstSize         int
}

type RateLimiterManager struct {
	config       RateLimitConfig
	rateLimiters map[string]*rate.Limiter
	mu           sync.RWMutex
}

func NewRateLimitConfigFromEnv() *RateLimitConfig {
	requestsPerMinuteStr := os.Getenv("RATE_LIMIT_REQUESTS_PER_MINUTE")
	if requestsPerMinuteStr == "" {
		requestsPerMinuteStr = "60" // Default to 60 requests per minute
	}

	requestsPerMinute, err := strconv.Atoi(requestsPerMinuteStr)
	if err != nil {
		log.Printf("Invalid RATE_LIMIT_REQUESTS_PER_MINUTE: %s, using default 60", requestsPerMinuteStr)
		requestsPerMinute = 60
	}

	// Allow burst of up to 10% of the per-minute limit, minimum 1
	burstSize := requestsPerMinute / 10
	if burstSize < 1 {
		burstSize = 1
	}

	log.Printf("Rate limiting configured: %d requests per minute, burst size: %d", requestsPerMinute, burstSize)

	return &RateLimitConfig{
		RequestsPerMinute: requestsPerMinute,
		BurstSize:         burstSize,
	}
}

func NewRateLimitConfig(requestsPerMinute int) *RateLimitConfig {
	burstSize := requestsPerMinute / 10
	burstSize = max(1, burstSize)

	return &RateLimitConfig{
		RequestsPerMinute: requestsPerMinute,
		BurstSize:         burstSize,
	}
}

func NewRateLimiterManager(config *RateLimitConfig) *RateLimiterManager {
	manager := &RateLimiterManager{
		config:       *config,
		rateLimiters: make(map[string]*rate.Limiter),
	}

	// Cleanup old rate limiters every 5 minutes
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			<-ticker.C
			manager.cleanupOldRateLimiters()
		}
	}()

	return manager
}

func (m *RateLimiterManager) cleanupOldRateLimiters() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Remove limiters that haven't been used recently
	// golang.org/x/time/rate handles token bucket cleanup internally
	// We just need to remove unused entries to prevent memory leaks

	// For simplicity, we'll clean up limiters that have full tokens
	// (indicating they haven't been used recently)
	for ip, limiter := range m.rateLimiters {
		// If the limiter has full tokens, it hasn't been used recently
		if limiter.Tokens() == float64(m.config.BurstSize) {
			delete(m.rateLimiters, ip)
		}
	}
}

func getRealIP(r *http.Request) string {
	// Check X-Forwarded-For header first (for proxies/load balancers)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP in the chain
		if ip := net.ParseIP(xff); ip != nil {
			return ip.String()
		}
	}

	// Check X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		if ip := net.ParseIP(xri); ip != nil {
			return ip.String()
		}
	}

	// Fall back to RemoteAddr
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (m *RateLimiterManager) getRateLimiter(ip string) *rate.Limiter {
	m.mu.Lock()
	defer m.mu.Unlock()

	limiter, exists := m.rateLimiters[ip]
	if !exists {
		// Create new rate limiter: rate per minute converted to rate per second
		ratePerSecond := rate.Limit(float64(m.config.RequestsPerMinute) / 60.0)
		limiter = rate.NewLimiter(ratePerSecond, m.config.BurstSize)
		m.rateLimiters[ip] = limiter
	}

	return limiter
}

func NewRateLimitMiddleware(manager *RateLimiterManager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := getRealIP(r)
			limiter := manager.getRateLimiter(ip)

			if !limiter.Allow() {
				log.Printf("🚨 RATE LIMIT: IP %s exceeded %d requests per minute", ip, manager.config.RequestsPerMinute)
				w.Header().Set("X-RateLimit-Limit", strconv.Itoa(manager.config.RequestsPerMinute))
				w.Header().Set("X-RateLimit-Window", "60")
				w.Header().Set("Retry-After", "60")
				http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RateLimitMiddleware creates a rate limit middleware using environment variables (for backward compatibility)
func RateLimitMiddleware(next http.Handler) http.Handler {
	config := NewRateLimitConfigFromEnv()
	manager := NewRateLimiterManager(config)
	return NewRateLimitMiddleware(manager)(next)
}
