package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/nickvd7/vaultrun/internal/metrics"
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
			metrics.RateLimitHitsTotal.WithLabelValues("ip").Inc()
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "rate limit exceeded",
			})
			return
		}
		c.Next()
	}
}

// ActorRateLimit returns middleware that limits requests per authenticated actor
// (API-key name). Must run AFTER APIKeyAuth so that Actor(c) is populated.
// The master actor is always exempt — it should never be throttled.
func ActorRateLimit(requestsPerMinute int) gin.HandlerFunc {
	l := newLimiter(requestsPerMinute)
	return func(c *gin.Context) {
		actor := Actor(c)
		// Master key and unauthenticated (already rejected by auth) are exempt.
		if actor == "master" || actor == "unknown" {
			c.Next()
			return
		}
		if !l.allow(actor) {
			metrics.RateLimitHitsTotal.WithLabelValues("actor").Inc()
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "rate limit exceeded",
			})
			return
		}
		c.Next()
	}
}

// NewRedisRateLimit returns a per-minute rate-limit middleware backed by Redis.
// Uses a sliding-window approximation: each 1-minute bucket has its own key.
// On Redis error the middleware fails open (allows the request) and logs a warning.
func NewRedisRateLimit(addr, password string, db, limit int) gin.HandlerFunc {
	opts := &redis.Options{Addr: addr, Password: password, DB: db}
	rdb := redis.NewClient(opts)
	return func(c *gin.Context) {
		ip := c.ClientIP()
		minute := time.Now().Unix() / 60
		key := fmt.Sprintf("ratelimit:ip:%s:%d", ip, minute)
		ctx := c.Request.Context()

		count, err := rdb.Incr(ctx, key).Result()
		if err != nil {
			slog.Warn("redis rate limit: incr failed, allowing request", "err", err)
			c.Next()
			return
		}
		if count == 1 {
			// First request in this window — set expiry.
			rdb.Expire(ctx, key, 90*time.Second)
		}
		if int(count) > limit {
			metrics.RateLimitHitsTotal.WithLabelValues("ip").Inc()
			c.Header("X-RateLimit-Limit", strconv.Itoa(limit))
			c.Header("Retry-After", "60")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			return
		}
		c.Header("X-RateLimit-Limit", strconv.Itoa(limit))
		c.Next()
	}
}

// NewRedisActorRateLimit is like NewRedisRateLimit but keys on the authenticated actor.
// Must run AFTER APIKeyAuth so that Actor(c) is populated.
// The master actor is always exempt — it should never be throttled.
func NewRedisActorRateLimit(addr, password string, db, limit int) gin.HandlerFunc {
	opts := &redis.Options{Addr: addr, Password: password, DB: db}
	rdb := redis.NewClient(opts)
	return func(c *gin.Context) {
		actor := Actor(c)
		// Master key and unauthenticated (already rejected by auth) are exempt.
		if actor == "master" || actor == "unknown" {
			c.Next()
			return
		}
		minute := time.Now().Unix() / 60
		key := fmt.Sprintf("ratelimit:actor:%s:%d", actor, minute)
		ctx := c.Request.Context()

		count, err := rdb.Incr(ctx, key).Result()
		if err != nil {
			slog.Warn("redis actor rate limit: incr failed, allowing request", "err", err)
			c.Next()
			return
		}
		if count == 1 {
			rdb.Expire(ctx, key, 90*time.Second)
		}
		if int(count) > limit {
			metrics.RateLimitHitsTotal.WithLabelValues("actor").Inc()
			c.Header("Retry-After", "60")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			return
		}
		c.Next()
	}
}

