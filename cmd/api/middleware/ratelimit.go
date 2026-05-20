package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// bucket is a simple token-bucket per key.
type bucket struct {
	tokens   float64
	capacity float64
	rate     float64 // tokens per second
	last     time.Time
	mu       sync.Mutex
}

func (b *bucket) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(b.last).Seconds()
	b.last = now

	b.tokens += elapsed * b.rate
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}

	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// limiter holds per-key buckets and runs periodic GC.
type limiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	capacity float64
	rate     float64
}

func newLimiter(requestsPerMinute int) *limiter {
	l := &limiter{
		buckets:  make(map[string]*bucket),
		capacity: float64(requestsPerMinute),
		rate:     float64(requestsPerMinute) / 60.0,
	}
	go l.gc()
	return l
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{
			tokens:   l.capacity,
			capacity: l.capacity,
			rate:     l.rate,
			last:     time.Now(),
		}
		l.buckets[key] = b
	}
	l.mu.Unlock()
	return b.allow()
}

// gc removes buckets that have been full (idle) for more than 10 minutes.
func (l *limiter) gc() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		l.mu.Lock()
		for key, b := range l.buckets {
			b.mu.Lock()
			idle := time.Since(b.last) > 10*time.Minute
			b.mu.Unlock()
			if idle {
				delete(l.buckets, key)
			}
		}
		l.mu.Unlock()
	}
}

// RateLimit returns middleware that allows at most requestsPerMinute requests
// per unique client IP. Exceeding callers receive 429 Too Many Requests.
func RateLimit(requestsPerMinute int) gin.HandlerFunc {
	l := newLimiter(requestsPerMinute)
	return func(c *gin.Context) {
		key := c.ClientIP()
		if !l.allow(key) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "rate limit exceeded",
			})
			return
		}
		c.Next()
	}
}
