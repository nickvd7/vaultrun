//go:build integration

package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/nickvd7/vaultrun/cmd/api/handlers"
	"github.com/nickvd7/vaultrun/cmd/api/middleware"
	"github.com/nickvd7/vaultrun/internal/audit"
	"github.com/nickvd7/vaultrun/internal/config"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	"github.com/nickvd7/vaultrun/internal/policy"
	"github.com/nickvd7/vaultrun/internal/workspace"
)

const testMasterKey = "integration-test-master-key"

var testDB *sqlx.DB

func TestMain(m *testing.M) {
	ctx := context.Background()

	// INTEGRATION_DSN lets CI supply a pre-provisioned Postgres (service
	// container). When not set we spin one up via testcontainers (needs Docker).
	dsn := os.Getenv("INTEGRATION_DSN")
	var cleanup func()

	if dsn == "" {
		pgC, err := postgres.Run(ctx,
			"postgres:16-alpine",
			postgres.WithDatabase("vaultrun_test"),
			postgres.WithUsername("vaultrun"),
			postgres.WithPassword("testpassword"),
			tc.WithWaitStrategy(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).
					WithStartupTimeout(60*time.Second),
			),
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "SKIP: cannot start postgres container (no Docker?): %v\n", err)
			os.Exit(0) // skip rather than fail — Docker not available in this env
		}
		cleanup = func() { _ = pgC.Terminate(ctx) }

		dsn, err = pgC.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			fmt.Fprintf(os.Stderr, "get connection string: %v\n", err)
			os.Exit(1)
		}
	}

	if cleanup != nil {
		defer cleanup()
	}

	db, err := dbpkg.Connect(config.DatabaseConfig{
		DSN:             dsn,
		MaxOpenConns:    5,
		MaxIdleConns:    2,
		ConnMaxLifetime: 5 * time.Minute,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect db: %v\n", err)
		os.Exit(1)
	}
	testDB = db

	migrationsPath := os.Getenv("MIGRATIONS_PATH")
	if migrationsPath == "" {
		migrationsPath = "../../../migrations"
	}
	if err := dbpkg.RunMigrations(db, migrationsPath); err != nil {
		fmt.Fprintf(os.Stderr, "run migrations: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// newTestRouter builds a minimal gin engine for integration tests.
// Docker and runner are nil — we only exercise DB-backed endpoints.
func newTestRouter(db *sqlx.DB) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())

	cfg := &config.Config{
		Auth:      config.AuthConfig{MasterKey: testMasterKey},
		Docker:    config.DockerConfig{DefaultImage: "python:3.12-slim"},
		Workspace: config.WorkspaceConfig{BaseDir: os.TempDir(), MaxFileMB: 100},
	}

	al := audit.New(db)
	ws := workspace.New(cfg.Workspace.BaseDir)
	hub := handlers.NewHub(db, nil, ws, nil, al, cfg, policy.AllowAll{}, nil)
	authMW := middleware.APIKeyAuth(db, testMasterKey)

	r.GET("/health", handlers.NewHealthHandler(hub).Health)

	api := r.Group("/api/v1", authMW)

	keysH := handlers.NewKeyHandler(hub)
	api.POST("/keys", keysH.Create)
	api.GET("/keys", keysH.List)
	api.DELETE("/keys/:id", keysH.Revoke)

	sessH := handlers.NewSessionHandler(hub)
	api.GET("/sessions", sessH.List)
	api.GET("/sessions/:id", sessH.Get)

	auditH := handlers.NewAuditHandler(hub)
	api.GET("/audit", auditH.List)

	return r
}

// truncateAll wipes every table so each test starts from an empty DB.
func truncateAll(t *testing.T) {
	t.Helper()
	_, err := testDB.Exec(`TRUNCATE audit_logs, files, runs, sessions, api_keys CASCADE`)
	if err != nil {
		t.Fatalf("truncate tables: %v", err)
	}
}

// rec fires an HTTP request at the test router and returns the recorder.
func rec(r *gin.Engine, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, bodyReader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func masterHdr() map[string]string { return map[string]string{"X-API-Key": testMasterKey} }

// ── health ───────────────────────────────────────────────────────────────────

func TestHealth(t *testing.T) {
	r := newTestRouter(testDB)
	w := rec(r, "GET", "/health", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var resp map[string]string
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "ok" {
		t.Errorf("want status=ok, got %q", resp["status"])
	}
}

// ── auth middleware ───────────────────────────────────────────────────────────

func TestAuthNoKey(t *testing.T) {
	r := newTestRouter(testDB)
	w := rec(r, "GET", "/api/v1/sessions", "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestAuthBadKey(t *testing.T) {
	r := newTestRouter(testDB)
	w := rec(r, "GET", "/api/v1/sessions", "", map[string]string{"X-API-Key": "vr_notakey"})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestAuthMasterKeyPasses(t *testing.T) {
	truncateAll(t)
	r := newTestRouter(testDB)
	w := rec(r, "GET", "/api/v1/sessions", "", masterHdr())
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
}

// ── key lifecycle ─────────────────────────────────────────────────────────────

func TestKeyCreateAndList(t *testing.T) {
	truncateAll(t)
	r := newTestRouter(testDB)

	w := rec(r, "POST", "/api/v1/keys", `{"name":"my-key"}`, masterHdr())
	if w.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d: %s", w.Code, w.Body)
	}
	var created map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created["key"] == nil || created["key"] == "" {
		t.Error("create response must include plaintext key")
	}
	if created["prefix"] == nil {
		t.Error("create response must include prefix")
	}
	if created["name"] != "my-key" {
		t.Errorf("name = %v, want my-key", created["name"])
	}

	w2 := rec(r, "GET", "/api/v1/keys", "", masterHdr())
	if w2.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d", w2.Code)
	}
	var list struct {
		APIKeys []map[string]interface{} `json:"api_keys"`
	}
	_ = json.NewDecoder(w2.Body).Decode(&list)
	if len(list.APIKeys) != 1 {
		t.Fatalf("want 1 key, got %d", len(list.APIKeys))
	}
	if list.APIKeys[0]["name"] != "my-key" {
		t.Errorf("listed name = %v, want my-key", list.APIKeys[0]["name"])
	}
}

func TestKeyRevoke(t *testing.T) {
	truncateAll(t)
	r := newTestRouter(testDB)

	w := rec(r, "POST", "/api/v1/keys", `{"name":"revoke-me"}`, masterHdr())
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var created map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&created)
	keyID := created["id"].(string)

	w2 := rec(r, "DELETE", "/api/v1/keys/"+keyID, "", masterHdr())
	if w2.Code != http.StatusNoContent {
		t.Fatalf("revoke: want 204, got %d: %s", w2.Code, w2.Body)
	}

	w3 := rec(r, "GET", "/api/v1/keys", "", masterHdr())
	var list struct {
		APIKeys []map[string]interface{} `json:"api_keys"`
	}
	_ = json.NewDecoder(w3.Body).Decode(&list)
	if len(list.APIKeys) != 1 {
		t.Fatalf("want 1 key in list after revoke, got %d", len(list.APIKeys))
	}
	if list.APIKeys[0]["active"].(bool) {
		t.Error("revoked key should have active=false")
	}
}

func TestKeyRevokeNotFound(t *testing.T) {
	truncateAll(t)
	r := newTestRouter(testDB)
	w := rec(r, "DELETE", "/api/v1/keys/00000000-0000-0000-0000-000000000001", "", masterHdr())
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestKeyRevokeBadUUID(t *testing.T) {
	r := newTestRouter(testDB)
	w := rec(r, "DELETE", "/api/v1/keys/not-a-uuid", "", masterHdr())
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestKeyExpiryPastRejected(t *testing.T) {
	r := newTestRouter(testDB)
	past := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	body := fmt.Sprintf(`{"name":"expired","expires_at":%q}`, past)
	w := rec(r, "POST", "/api/v1/keys", body, masterHdr())
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for past expiry, got %d: %s", w.Code, w.Body)
	}
}

func TestKeyExpiryFutureAccepted(t *testing.T) {
	truncateAll(t)
	r := newTestRouter(testDB)
	future := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	body := fmt.Sprintf(`{"name":"expires-soon","expires_at":%q}`, future)
	w := rec(r, "POST", "/api/v1/keys", body, masterHdr())
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body)
	}
	var resp map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["expires_at"] == nil {
		t.Error("response must include expires_at when set")
	}
}

func TestUserKeyAuthenticates(t *testing.T) {
	truncateAll(t)
	r := newTestRouter(testDB)

	w := rec(r, "POST", "/api/v1/keys", `{"name":"user-key"}`, masterHdr())
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d", w.Code)
	}
	var created map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&created)
	plainKey := created["key"].(string)

	w2 := rec(r, "GET", "/api/v1/sessions", "", map[string]string{"X-API-Key": plainKey})
	if w2.Code != http.StatusOK {
		t.Fatalf("user key: want 200, got %d: %s", w2.Code, w2.Body)
	}
}

func TestRevokedKeyRejected(t *testing.T) {
	truncateAll(t)
	r := newTestRouter(testDB)

	// Create and immediately revoke a key
	w := rec(r, "POST", "/api/v1/keys", `{"name":"soon-revoked"}`, masterHdr())
	var created map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&created)
	plainKey := created["key"].(string)
	keyID := created["id"].(string)

	rec(r, "DELETE", "/api/v1/keys/"+keyID, "", masterHdr())

	// Now the revoked key should not authenticate
	w2 := rec(r, "GET", "/api/v1/sessions", "", map[string]string{"X-API-Key": plainKey})
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("revoked key: want 401, got %d", w2.Code)
	}
}

// ── sessions ──────────────────────────────────────────────────────────────────

func TestSessionListEmpty(t *testing.T) {
	truncateAll(t)
	r := newTestRouter(testDB)
	w := rec(r, "GET", "/api/v1/sessions", "", masterHdr())
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var resp struct {
		Sessions []interface{} `json:"sessions"`
	}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Sessions) != 0 {
		t.Errorf("want 0 sessions, got %d", len(resp.Sessions))
	}
}

func TestSessionGetNotFound(t *testing.T) {
	r := newTestRouter(testDB)
	w := rec(r, "GET", "/api/v1/sessions/00000000-0000-0000-0000-000000000099", "", masterHdr())
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestSessionGetBadUUID(t *testing.T) {
	r := newTestRouter(testDB)
	w := rec(r, "GET", "/api/v1/sessions/not-a-uuid", "", masterHdr())
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

// ── audit ─────────────────────────────────────────────────────────────────────

func TestAuditListEmpty(t *testing.T) {
	truncateAll(t)
	r := newTestRouter(testDB)
	w := rec(r, "GET", "/api/v1/audit", "", masterHdr())
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var resp struct {
		Logs []interface{} `json:"audit_logs"`
	}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Logs) != 0 {
		t.Errorf("want 0 audit logs, got %d", len(resp.Logs))
	}
}

func TestAuditRecordsKeyCreate(t *testing.T) {
	truncateAll(t)
	r := newTestRouter(testDB)

	w := rec(r, "POST", "/api/v1/keys", `{"name":"audit-key"}`, masterHdr())
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d", w.Code)
	}

	w2 := rec(r, "GET", "/api/v1/audit", "", masterHdr())
	if w2.Code != http.StatusOK {
		t.Fatalf("audit list: %d", w2.Code)
	}
	var resp struct {
		Logs []struct {
			Action string `json:"action"`
		} `json:"audit_logs"`
	}
	_ = json.NewDecoder(w2.Body).Decode(&resp)

	found := false
	for _, l := range resp.Logs {
		if l.Action == "apikey.created" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected apikey.created audit log after key creation")
	}
}

func TestAuditRecordsKeyRevoke(t *testing.T) {
	truncateAll(t)
	r := newTestRouter(testDB)

	w := rec(r, "POST", "/api/v1/keys", `{"name":"revoke-audit"}`, masterHdr())
	var created map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&created)
	keyID := created["id"].(string)

	rec(r, "DELETE", "/api/v1/keys/"+keyID, "", masterHdr())

	w2 := rec(r, "GET", "/api/v1/audit", "", masterHdr())
	var resp struct {
		Logs []struct {
			Action string `json:"action"`
		} `json:"audit_logs"`
	}
	_ = json.NewDecoder(w2.Body).Decode(&resp)

	found := false
	for _, l := range resp.Logs {
		if l.Action == "apikey.revoked" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected apikey.revoked audit log after key revocation")
	}
}
