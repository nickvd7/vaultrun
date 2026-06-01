package middleware

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func init() { gin.SetMode(gin.TestMode) }

// --- bucket unit tests ---

func TestBucketAllowUpToCapacity(t *testing.T) {
	b := &bucket{tokens: 3, capacity: 3, rate: 1, last: time.Now()}
	for i := range 3 {
		if !b.allow() {
			t.Fatalf("expected allow on call %d", i)
		}
	}
	if b.allow() {
		t.Fatal("expected deny after capacity exhausted")
	}
}

func TestBucketRefillOverTime(t *testing.T) {
	// Start empty, set last to 2 seconds ago so refill happens on next allow().
	b := &bucket{tokens: 0, capacity: 5, rate: 1, last: time.Now().Add(-2 * time.Second)}
	// After 2 seconds at rate=1, we should have 2 tokens.
	if !b.allow() {
		t.Fatal("expected allow after refill")
	}
	if !b.allow() {
		t.Fatal("expected second allow after refill")
	}
	if b.allow() {
		t.Fatal("expected deny after consuming refilled tokens")
	}
}

func TestBucketConcurrentSafety(t *testing.T) {
	b := &bucket{tokens: 100, capacity: 100, rate: 0, last: time.Now()}
	var allowed atomic.Int64
	var wg sync.WaitGroup
	for range 200 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if b.allow() {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if allowed.Load() != 100 {
		t.Fatalf("expected exactly 100 allows, got %d", allowed.Load())
	}
}

// --- limiter unit tests ---

func TestLimiterPerKeyBuckets(t *testing.T) {
	l := newLimiter(60) // 60 req/min = 1/sec, capacity 60
	// Each key should get its own bucket starting full.
	if !l.allow("a") {
		t.Fatal("expected first allow for key a")
	}
	if !l.allow("b") {
		t.Fatal("expected first allow for key b")
	}
}

func TestLimiterExhaustsPerKey(t *testing.T) {
	l := &limiter{
		buckets:  make(map[string]*bucket),
		capacity: 2,
		rate:     0, // no refill during test
	}
	if !l.allow("x") {
		t.Fatal("first")
	}
	if !l.allow("x") {
		t.Fatal("second")
	}
	if l.allow("x") {
		t.Fatal("third should be denied")
	}
	// Different key is unaffected
	if !l.allow("y") {
		t.Fatal("key y should still be allowed")
	}
}

// --- HTTP middleware integration tests ---

func newTestRouter(rpm int) *gin.Engine {
	r := gin.New()
	r.GET("/ping", RateLimit(rpm), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	return r
}

func TestRateLimitMiddlewareAllows(t *testing.T) {
	r := newTestRouter(60)
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.RemoteAddr = "1.2.3.4:9999"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestRateLimitMiddlewareBlocks(t *testing.T) {
	// capacity = 2 so third request from same IP gets 429
	l := &limiter{buckets: make(map[string]*bucket), capacity: 2, rate: 0}
	r := gin.New()
	r.GET("/ping", func(c *gin.Context) {
		if !l.allow(c.ClientIP()) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			return
		}
		c.Status(http.StatusOK)
	})

	ip := "5.6.7.8:1234"
	for i, wantCode := range []int{200, 200, 429} {
		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		req.RemoteAddr = ip
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != wantCode {
			t.Fatalf("request %d: expected %d, got %d", i, wantCode, w.Code)
		}
	}
}

func TestRateLimitIsolatedByIP(t *testing.T) {
	l := &limiter{buckets: make(map[string]*bucket), capacity: 1, rate: 0}
	handler := func(c *gin.Context) {
		if !l.allow(c.ClientIP()) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			return
		}
		c.Status(http.StatusOK)
	}
	r := gin.New()
	r.GET("/ping", handler)

	for _, ip := range []string{"10.0.0.1:1", "10.0.0.2:1", "10.0.0.3:1"} {
		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		req.RemoteAddr = ip
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("ip %s: expected 200, got %d", ip, w.Code)
		}
	}
}

// TestRedisRateLimitFallback verifies that when Redis is unreachable the
// in-memory fallback limiter still enforces the rate limit rather than
// allowing all requests through (fail-open).
func TestRedisRateLimitFallback(t *testing.T) {
	// Port 19999 is almost certainly not listening — Redis calls will error.
	h := NewRedisRateLimit("127.0.0.1:19999", "", 0, 1)
	r := gin.New()
	r.GET("/ping", h, func(c *gin.Context) { c.Status(http.StatusOK) })

	ip := "9.8.7.6:1234"

	req1 := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req1.RemoteAddr = ip
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first request (within fallback limit): want 200, got %d", w1.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req2.RemoteAddr = ip
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request (exceeds fallback limit=1): want 429, got %d", w2.Code)
	}
}

// TestRedisActorRateLimitFallback mirrors TestRedisRateLimitFallback for the
// actor-keyed variant.
func TestRedisActorRateLimitFallback(t *testing.T) {
	h := NewRedisActorRateLimit("127.0.0.1:19999", "", 0, 1)
	r := gin.New()
	// Inject an actor directly since we're not running through APIKeyAuth.
	r.GET("/ping",
		func(c *gin.Context) { c.Set("actor", "test-actor"); c.Set("actor_name", "test-actor"); c.Next() },
		h,
		func(c *gin.Context) { c.Status(http.StatusOK) },
	)

	req1 := httptest.NewRequest(http.MethodGet, "/ping", nil)
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first request: want 200, got %d", w1.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/ping", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: want 429, got %d", w2.Code)
	}
}
