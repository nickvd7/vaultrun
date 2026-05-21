// Package metrics defines and exposes all Prometheus metrics for VaultRun.
// Metrics are registered against the default registry and are automatically
// available via the /metrics HTTP endpoint mounted in the router.
package metrics

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// ActiveSessions is the current number of non-stopped sessions.
	ActiveSessions = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "vaultrun",
		Name:      "sessions_active",
		Help:      "Number of currently active (running) sessions.",
	})

	// RunsTotal counts completed runs partitioned by terminal status.
	RunsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "vaultrun",
		Name:      "runs_total",
		Help:      "Total number of sandbox runs by terminal status (completed|failed|timeout).",
	}, []string{"status"})

	// RunDurationSeconds measures how long runs take.
	RunDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "vaultrun",
		Name:      "run_duration_seconds",
		Help:      "Sandbox run wall-clock duration in seconds.",
		Buckets:   []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60, 120, 300},
	}, []string{"status"})

	// HTTPRequestsTotal counts all HTTP responses by method and status code.
	HTTPRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "vaultrun",
		Name:      "http_requests_total",
		Help:      "Total HTTP requests by method and response status code.",
	}, []string{"method", "status_code"})

	// HTTPRequestDurationSeconds measures HTTP handler latency.
	// The path label uses a normalised route pattern (e.g. /api/v1/sessions/:id)
	// rather than the raw URL to avoid high cardinality from UUID path params.
	HTTPRequestDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "vaultrun",
		Name:      "http_request_duration_seconds",
		Help:      "HTTP request duration in seconds by method and route.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"method", "route"})

	// ContainerCreationsTotal counts container create attempts by outcome.
	// Labels: status = "created" | "failed"
	ContainerCreationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "vaultrun",
		Name:      "container_creations_total",
		Help:      "Total sandbox container creation attempts, partitioned by outcome.",
	}, []string{"status"})

	// ContainerStopsTotal counts container stop/remove calls.
	ContainerStopsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "vaultrun",
		Name:      "container_stops_total",
		Help:      "Total number of sandbox containers stopped and removed.",
	})

	// FilesUploadedTotal counts files uploaded to session workspaces.
	FilesUploadedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "vaultrun",
		Name:      "files_uploaded_total",
		Help:      "Total number of files uploaded to session workspaces.",
	})

	// FileBytesUploadedTotal accumulates the total bytes uploaded.
	FileBytesUploadedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "vaultrun",
		Name:      "file_bytes_uploaded_total",
		Help:      "Total bytes uploaded to session workspaces.",
	})

	// JobQueueDepth is the current approximate number of pending async run jobs.
	// Updated periodically by a background goroutine in main.
	JobQueueDepth = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "vaultrun",
		Name:      "job_queue_depth",
		Help:      "Approximate number of async run jobs currently waiting in the queue.",
	})

	// RateLimitHitsTotal counts requests rejected by the rate limiter.
	// Labels: type = "ip" (IP-based) | "actor" (per-API-key)
	RateLimitHitsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "vaultrun",
		Name:      "rate_limit_hits_total",
		Help:      "Total requests rejected by the rate limiter, by limiter type.",
	}, []string{"type"})
)

// ObserveRun records run completion metrics. Call after the run status is finalised.
func ObserveRun(status string, durationMS int64) {
	RunsTotal.WithLabelValues(status).Inc()
	RunDurationSeconds.WithLabelValues(status).Observe(float64(durationMS) / 1000.0)
}

// HTTPMiddleware returns a Gin middleware that records request count and
// latency for every handler. It uses the matched route pattern as the
// path label to avoid high cardinality from UUID parameters.
func HTTPMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		route := c.FullPath() // e.g. "/api/v1/sessions/:id" — low cardinality
		if route == "" {
			route = "unmatched"
		}
		elapsed := time.Since(start).Seconds()
		status := strconv.Itoa(c.Writer.Status())

		HTTPRequestsTotal.WithLabelValues(c.Request.Method, status).Inc()
		HTTPRequestDurationSeconds.WithLabelValues(c.Request.Method, route).Observe(elapsed)
	}
}
