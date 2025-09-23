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

var (
	rateLimiters = make(map[string]*rate.Limiter)
	rateLimitMu  sync.RWMutex
	rlConfig     RateLimitConfig
)

func setupRateLimit() {
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

	rlConfig = RateLimitConfig{
		RequestsPerMinute: requestsPerMinute,
		BurstSize:         burstSize,
	}

	log.Printf("Rate limiting configured: %d requests per minute, burst size: %d", requestsPerMinute, burstSize)

	// Cleanup old rate limiters every 5 minutes
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			<-ticker.C
			cleanupOldRateLimiters()
		}
	}()
}

func cleanupOldRateLimiters() {
	rateLimitMu.Lock()
	defer rateLimitMu.Unlock()

	// Remove limiters that haven't been used recently
	// golang.org/x/time/rate handles token bucket cleanup internally
	// We just need to remove unused entries to prevent memory leaks

	// For simplicity, we'll clean up limiters that have full tokens
	// (indicating they haven't been used recently)
	for ip, limiter := range rateLimiters {
		// If the limiter has full tokens, it hasn't been used recently
		if limiter.Tokens() == float64(rlConfig.BurstSize) {
			delete(rateLimiters, ip)
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

func getRateLimiter(ip string) *rate.Limiter {
	rateLimitMu.Lock()
	defer rateLimitMu.Unlock()

	limiter, exists := rateLimiters[ip]
	if !exists {
		// Create new rate limiter: rate per minute converted to rate per second
		ratePerSecond := rate.Limit(float64(rlConfig.RequestsPerMinute) / 60.0)
		limiter = rate.NewLimiter(ratePerSecond, rlConfig.BurstSize)
		rateLimiters[ip] = limiter
	}

	return limiter
}

func RateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := getRealIP(r)
		limiter := getRateLimiter(ip)

		if !limiter.Allow() {
			log.Printf("🚨 RATE LIMIT: IP %s exceeded %d requests per minute", ip, rlConfig.RequestsPerMinute)
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(rlConfig.RequestsPerMinute))
			w.Header().Set("X-RateLimit-Window", "60")
			w.Header().Set("Retry-After", "60")
			http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func init() {
	setupRateLimit()
}
