package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"

	"github.com/nickvd7/vaultrun/internal/config"
	"github.com/nickvd7/vaultrun/internal/jobqueue"
)

// fakeQueue is a minimal jobqueue.Queue stub that reports a fixed length.
type fakeQueue struct {
	length int
}

func (f *fakeQueue) Submit(jobqueue.Job) bool    { return true }
func (f *fakeQueue) Len() int                    { return f.length }
func (f *fakeQueue) Drain(_ context.Context)     {}

func newInMemoryDB(t *testing.T) *sqlx.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := sqlDB.Ping(); err != nil {
		t.Fatalf("ping sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return sqlx.NewDb(sqlDB, "sqlite")
}

func newHealthTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/health", nil)
	return c, w
}

func decodeHealthResponse(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response %q: %v", w.Body.String(), err)
	}
	return out
}

func TestHealthAllOK(t *testing.T) {
	db := newInMemoryDB(t)
	h := &Hub{db: db, cfg: &config.Config{}}
	hh := NewHealthHandler(h)

	c, w := newHealthTestContext()
	hh.Health(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	got := decodeHealthResponse(t, w)
	if got["status"] != "ok" {
		t.Errorf("status = %v, want ok", got["status"])
	}
	checks, _ := got["checks"].(map[string]any)
	db_, _ := checks["database"].(map[string]any)
	if db_["status"] != "ok" {
		t.Errorf("database check = %+v, want ok", db_)
	}
	docker_, _ := checks["docker"].(map[string]any)
	if docker_["status"] != "not_configured" {
		t.Errorf("docker check = %+v, want not_configured", docker_)
	}
	if _, ok := checks["job_queue"]; ok {
		t.Errorf("expected no job_queue check when queue is nil, got %+v", checks["job_queue"])
	}
	if _, ok := got["region"]; ok {
		t.Errorf("expected no region field when MultiRegion.Region is empty, got %+v", got)
	}
}

func TestHealthDatabaseDownReturnsDegraded(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db := sqlx.NewDb(sqlDB, "sqlite")
	// Closing the DB makes subsequent pings fail, simulating an outage.
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	h := &Hub{db: db, cfg: &config.Config{}}
	hh := NewHealthHandler(h)

	c, w := newHealthTestContext()
	hh.Health(c)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	got := decodeHealthResponse(t, w)
	if got["status"] != "degraded" {
		t.Errorf("status = %v, want degraded", got["status"])
	}
	checks, _ := got["checks"].(map[string]any)
	db_, _ := checks["database"].(map[string]any)
	if db_["status"] != "error" {
		t.Errorf("database check = %+v, want error", db_)
	}
	if _, ok := db_["error"]; !ok {
		t.Error("expected database error detail in response")
	}
}

func TestHealthQueueOKReportsPendingCount(t *testing.T) {
	db := newInMemoryDB(t)
	h := &Hub{db: db, cfg: &config.Config{}, queue: &fakeQueue{length: 7}}
	hh := NewHealthHandler(h)

	c, w := newHealthTestContext()
	hh.Health(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	got := decodeHealthResponse(t, w)
	checks, _ := got["checks"].(map[string]any)
	q, _ := checks["job_queue"].(map[string]any)
	if q["status"] != "ok" {
		t.Errorf("job_queue status = %v, want ok", q["status"])
	}
	if pending, _ := q["pending"].(float64); pending != 7 {
		t.Errorf("job_queue pending = %v, want 7", q["pending"])
	}
}

func TestHealthQueueUnreachableReturnsDegraded(t *testing.T) {
	db := newInMemoryDB(t)
	h := &Hub{db: db, cfg: &config.Config{}, queue: &fakeQueue{length: -1}}
	hh := NewHealthHandler(h)

	c, w := newHealthTestContext()
	hh.Health(c)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	got := decodeHealthResponse(t, w)
	if got["status"] != "degraded" {
		t.Errorf("status = %v, want degraded", got["status"])
	}
	checks, _ := got["checks"].(map[string]any)
	q, _ := checks["job_queue"].(map[string]any)
	if q["status"] != "error" {
		t.Errorf("job_queue status = %v, want error", q["status"])
	}
}

func TestHealthIncludesRegionWhenConfigured(t *testing.T) {
	db := newInMemoryDB(t)
	cfg := &config.Config{}
	cfg.MultiRegion.Region = "eu-west-1"
	h := &Hub{db: db, cfg: cfg}
	hh := NewHealthHandler(h)

	c, w := newHealthTestContext()
	hh.Health(c)

	got := decodeHealthResponse(t, w)
	if got["region"] != "eu-west-1" {
		t.Errorf("region = %v, want eu-west-1", got["region"])
	}
}

func TestHealthRespectsContextTimeout(t *testing.T) {
	db := newInMemoryDB(t)
	h := &Hub{db: db, cfg: &config.Config{}}
	hh := NewHealthHandler(h)

	c, w := newHealthTestContext()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Millisecond)
	defer cancel()
	c.Request = c.Request.WithContext(ctx)

	// Health derives its own 3s timeout from the request context; an already
	// short-lived parent context must not cause a panic and must still
	// produce a well-formed response.
	hh.Health(c)

	if w.Code != http.StatusOK && w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unexpected status %d", w.Code)
	}
	_ = decodeHealthResponse(t, w)
}
