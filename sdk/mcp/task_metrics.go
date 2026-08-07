package main

import (
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	mcpTasksStarted = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "vaultrun_mcp",
		Name:      "tasks_started_total",
		Help:      "MCP Tasks created (async tool starts).",
	}, []string{"tool", "backend"})

	mcpTasksTerminal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "vaultrun_mcp",
		Name:      "tasks_terminal_total",
		Help:      "MCP Tasks reaching a terminal status.",
	}, []string{"status", "backend"})

	mcpTasksCancelled = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "vaultrun_mcp",
		Name:      "tasks_cancelled_total",
		Help:      "MCP Tasks cancel requests that transitioned a working task.",
	})

	mcpTasksInputRequired = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "vaultrun_mcp",
		Name:      "tasks_input_required_total",
		Help:      "Times a task entered input_required.",
	})

	mcpTasksInflight = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "vaultrun_mcp",
		Name:      "tasks_inflight",
		Help:      "Current non-terminal MCP Tasks.",
	}, []string{"backend"})

	mcpTasksTTLEvicted = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "vaultrun_mcp",
		Name:      "tasks_ttl_evicted_total",
		Help:      "Finished tasks removed by TTL cleanup (memory backend).",
	})

	mcpTasksMaxAgeFailed = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "vaultrun_mcp",
		Name:      "tasks_max_age_failed_total",
		Help:      "Working tasks force-failed after max age.",
	})

	mcpTasksRedisFallback = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "vaultrun_mcp",
		Name:      "tasks_redis_fallback_total",
		Help:      "Times MCP Tasks fell back to in-memory because Redis was unreachable.",
	})

	mcpTasksInflightRejected = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "vaultrun_mcp",
		Name:      "tasks_inflight_rejected_total",
		Help:      "Async starts rejected by the in-flight cap.",
	})
)

func (ts *taskStore) backendLabel() string {
	if ts != nil && ts.redisEnabled() {
		return "redis"
	}
	return "memory"
}

func (ts *taskStore) observeStarted(tool string) {
	backend := ts.backendLabel()
	mcpTasksStarted.WithLabelValues(tool, backend).Inc()
	mcpTasksInflight.WithLabelValues(backend).Inc()
	slog.Info("vaultrun-mcp: task started", "tool", tool, "backend", backend)
}

func (ts *taskStore) observeTerminal(status taskStatus) {
	backend := ts.backendLabel()
	mcpTasksTerminal.WithLabelValues(string(status), backend).Inc()
	mcpTasksInflight.WithLabelValues(backend).Dec()
	slog.Info("vaultrun-mcp: task terminal", "status", string(status), "backend", backend)
}

func observeTaskCancel() {
	mcpTasksCancelled.Inc()
	slog.Info("vaultrun-mcp: task cancelled")
}

func observeTaskInputRequired() {
	mcpTasksInputRequired.Inc()
	slog.Debug("vaultrun-mcp: task input_required")
}

func observeTaskTTLEvicted(n int) {
	if n <= 0 {
		return
	}
	mcpTasksTTLEvicted.Add(float64(n))
	slog.Info("vaultrun-mcp: task TTL evicted", "count", n)
}

func observeTaskMaxAgeFailed() {
	mcpTasksMaxAgeFailed.Inc()
	slog.Info("vaultrun-mcp: task max-age failed")
}

func observeRedisFallback(addr string, err error) {
	mcpTasksRedisFallback.Inc()
	slog.Warn("vaultrun-mcp: Redis unreachable, using in-memory tasks",
		"addr", addr, "err", err)
}

func observeInflightRejected(max int) {
	mcpTasksInflightRejected.Inc()
	slog.Warn("vaultrun-mcp: task inflight cap reached", "max", max)
}
