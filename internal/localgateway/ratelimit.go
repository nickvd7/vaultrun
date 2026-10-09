package localgateway

import (
	"sync"
	"time"
)

// ipRateLimiter is a fixed one-minute sliding window per IP (same idea as MCP).
type ipRateLimiter struct {
	mu      sync.Mutex
	limit   int
	windows map[string][]time.Time
}

func newIPRateLimiter(limit int) *ipRateLimiter {
	return &ipRateLimiter{
		limit:   limit,
		windows: make(map[string][]time.Time),
	}
}

func (r *ipRateLimiter) allow(ip string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-time.Minute)
	ts := r.windows[ip]
	fresh := ts[:0]
	for _, t := range ts {
		if t.After(cutoff) {
			fresh = append(fresh, t)
		}
	}
	if len(fresh) >= r.limit {
		r.windows[ip] = fresh
		return false
	}
	r.windows[ip] = append(fresh, now)
	if len(r.windows) > 10_000 {
		r.sweepLocked(cutoff)
	}
	return true
}

func (r *ipRateLimiter) sweepLocked(cutoff time.Time) {
	for ip, ts := range r.windows {
		keep := false
		for _, t := range ts {
			if t.After(cutoff) {
				keep = true
				break
			}
		}
		if !keep {
			delete(r.windows, ip)
		}
	}
}
