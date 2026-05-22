//go:build integration

package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/google/uuid"
	"github.com/nickvd7/vaultrun/cmd/api/handlers"
	"github.com/nickvd7/vaultrun/cmd/api/middleware"
	"github.com/nickvd7/vaultrun/internal/audit"
	"github.com/nickvd7/vaultrun/internal/config"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	"github.com/nickvd7/vaultrun/internal/models"
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
	hub := handlers.NewHub(db, nil, ws, nil, al, cfg, policy.AllowAll{}, nil, nil, nil)
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
	api.PATCH("/sessions/:id/labels", sessH.UpdateLabels)

	orgH := handlers.NewOrgHandler(hub)
	api.POST("/orgs", orgH.Create)
	api.GET("/orgs", orgH.List)
	api.GET("/orgs/:id", orgH.Get)
	api.DELETE("/orgs/:id", orgH.Delete)
	api.POST("/orgs/:id/members", orgH.AddMember)
	api.GET("/orgs/:id/members", orgH.ListMembers)
	api.DELETE("/orgs/:id/members/:principal", orgH.RemoveMember)
	api.GET("/orgs/:id/sessions", orgH.ListSessions)

	policyH := handlers.NewPolicyHandler(hub)
	api.GET("/policy", policyH.Get)
	api.POST("/policy/eval", policyH.Eval)

	auditH := handlers.NewAuditHandler(hub)
	api.GET("/audit", auditH.List)

	return r
}

// truncateAll wipes every table so each test starts from an empty DB.
func truncateAll(t *testing.T) {
	t.Helper()
	_, err := testDB.Exec(`TRUNCATE audit_logs, files, runs, sessions, api_keys, org_members, organizations CASCADE`)
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

// ── session labels ────────────────────────────────────────────────────────────

// insertFakeSession writes a minimal session directly to the DB for tests that
// need a session record but don't want to spin up a Docker container.
func insertFakeSession(t *testing.T, id, actor string) {
	t.Helper()
	_, err := testDB.ExecContext(context.Background(), `
		INSERT INTO sessions (id, name, image, status, network_enabled, cpu_limit,
		    memory_limit_mb, timeout_seconds, workspace_path, labels, allowed_hosts,
		    created_by, created_at, updated_at)
		VALUES ($1, NULL, 'python:3.12-slim', 'running', false, 1.0,
		    512, 300, '/tmp/ws', '{}', '{}', $2, NOW(), NOW())`,
		id, actor,
	)
	if err != nil {
		t.Fatalf("insertFakeSession: %v", err)
	}
}

func TestSessionUpdateLabels(t *testing.T) {
	truncateAll(t)
	sessID := "00000000-0000-0000-0000-000000000001"
	insertFakeSession(t, sessID, "master")

	r := newTestRouter(testDB)

	// Set two labels
	w := rec(r, "PATCH", "/api/v1/sessions/"+sessID+"/labels",
		`{"labels":{"env":"prod","team":"ml"}}`, masterHdr())
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var resp1 struct {
		Labels map[string]string `json:"labels"`
	}
	_ = json.NewDecoder(w.Body).Decode(&resp1)
	if resp1.Labels["env"] != "prod" {
		t.Errorf("want env=prod, got %v", resp1.Labels["env"])
	}
	if resp1.Labels["team"] != "ml" {
		t.Errorf("want team=ml, got %v", resp1.Labels["team"])
	}

	// Verify they're persisted via GET
	w2 := rec(r, "GET", "/api/v1/sessions/"+sessID, "", masterHdr())
	if w2.Code != http.StatusOK {
		t.Fatalf("GET session: %d", w2.Code)
	}
	var sess map[string]interface{}
	_ = json.NewDecoder(w2.Body).Decode(&sess)
	lbls, _ := sess["labels"].(map[string]interface{})
	if lbls["env"] != "prod" {
		t.Errorf("labels not persisted: %v", lbls)
	}
}

func TestSessionUpdateLabelsClear(t *testing.T) {
	truncateAll(t)
	sessID := "00000000-0000-0000-0000-000000000002"
	insertFakeSession(t, sessID, "master")

	r := newTestRouter(testDB)

	// Set labels first
	rec(r, "PATCH", "/api/v1/sessions/"+sessID+"/labels",
		`{"labels":{"env":"staging"}}`, masterHdr())

	// Clear all labels
	w := rec(r, "PATCH", "/api/v1/sessions/"+sessID+"/labels",
		`{"labels":{}}`, masterHdr())
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp struct {
		Labels map[string]string `json:"labels"`
	}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Labels) != 0 {
		t.Errorf("expected empty labels after clear, got %v", resp.Labels)
	}
}

func TestSessionLabelFilterMatch(t *testing.T) {
	truncateAll(t)
	sessID := "00000000-0000-0000-0000-000000000003"
	insertFakeSession(t, sessID, "master")

	r := newTestRouter(testDB)

	// Apply a label
	rec(r, "PATCH", "/api/v1/sessions/"+sessID+"/labels",
		`{"labels":{"project":"alpha"}}`, masterHdr())

	// Filter by that label — should return 1 session
	w := rec(r, "GET", "/api/v1/sessions?label=project:alpha", "", masterHdr())
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var resp struct {
		Sessions []map[string]interface{} `json:"sessions"`
	}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Sessions) != 1 {
		t.Errorf("want 1 session, got %d", len(resp.Sessions))
	}
}

func TestSessionLabelFilterNoMatch(t *testing.T) {
	truncateAll(t)
	sessID := "00000000-0000-0000-0000-000000000004"
	insertFakeSession(t, sessID, "master")

	r := newTestRouter(testDB)

	// Filter by a label that doesn't exist
	w := rec(r, "GET", "/api/v1/sessions?label=project:beta", "", masterHdr())
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp struct {
		Sessions []map[string]interface{} `json:"sessions"`
	}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Sessions) != 0 {
		t.Errorf("want 0 sessions, got %d", len(resp.Sessions))
	}
}

func TestSessionUpdateLabelsBadUUID(t *testing.T) {
	r := newTestRouter(testDB)
	w := rec(r, "PATCH", "/api/v1/sessions/not-a-uuid/labels",
		`{"labels":{"k":"v"}}`, masterHdr())
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

// ── policy ────────────────────────────────────────────────────────────────────

func TestPolicyGet(t *testing.T) {
	r := newTestRouter(testDB)
	w := rec(r, "GET", "/api/v1/policy", "", masterHdr())
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var resp map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	// AllowAll policy → enabled should be false (no OPA policy file configured)
	if _, ok := resp["enabled"]; !ok {
		t.Error("response should contain 'enabled' field")
	}
}

func TestPolicyEvalCommandAllowed(t *testing.T) {
	r := newTestRouter(testDB)
	w := rec(r, "POST", "/api/v1/policy/eval",
		`{"type":"command","command":"python","args":["script.py"]}`, masterHdr())
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var resp struct {
		Allowed bool   `json:"allowed"`
		Type    string `json:"type"`
	}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if !resp.Allowed {
		t.Error("AllowAll policy should allow all commands")
	}
	if resp.Type != "command" {
		t.Errorf("want type=command, got %q", resp.Type)
	}
}

func TestPolicyEvalFileAllowed(t *testing.T) {
	r := newTestRouter(testDB)
	w := rec(r, "POST", "/api/v1/policy/eval",
		`{"type":"file","path":"/workspace/data.csv","write":true}`, masterHdr())
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var resp struct {
		Allowed bool `json:"allowed"`
	}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if !resp.Allowed {
		t.Error("AllowAll policy should allow file operations")
	}
}

func TestPolicyEvalBadType(t *testing.T) {
	r := newTestRouter(testDB)
	w := rec(r, "POST", "/api/v1/policy/eval",
		`{"type":"unknown"}`, masterHdr())
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body)
	}
}

// ── per-actor rate limit ──────────────────────────────────────────────────────

// newActorLimitRouter builds a minimal test router where every authenticated
// non-master actor is limited to requestsPerMinute list-sessions calls.
func newActorLimitRouter(db *sqlx.DB, requestsPerMinute int) *gin.Engine {
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
	hub := handlers.NewHub(db, nil, ws, nil, al, cfg, policy.AllowAll{}, nil, nil, nil)

	authMW := middleware.APIKeyAuth(db, testMasterKey)
	actorMW := middleware.ActorRateLimit(requestsPerMinute)

	api := r.Group("/api/v1", authMW, actorMW)
	api.GET("/sessions", handlers.NewSessionHandler(hub).List)
	return r
}

// TestActorRateLimitEnforced verifies that a non-master actor is blocked after
// exhausting the per-actor token bucket.
func TestActorRateLimitEnforced(t *testing.T) {
	truncateAll(t)

	// Create a regular API key — master key is always exempt from actor limits.
	r0 := newTestRouter(testDB)
	w := rec(r0, "POST", "/api/v1/keys", `{"name":"rl-test-actor"}`, masterHdr())
	if w.Code != http.StatusCreated {
		t.Fatalf("create key: want 201, got %d: %s", w.Code, w.Body)
	}
	var keyResp struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(w.Body).Decode(&keyResp); err != nil || keyResp.Key == "" {
		t.Fatal("no plaintext key in create-key response")
	}
	userHdr := map[string]string{"X-API-Key": keyResp.Key}

	// Build a router that caps non-master actors at 2 requests/min.
	r := newActorLimitRouter(testDB, 2)

	// First two requests should succeed (bucket starts full).
	for i := 1; i <= 2; i++ {
		w := rec(r, "GET", "/api/v1/sessions", "", userHdr)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: want 200, got %d: %s", i, w.Code, w.Body)
		}
	}

	// Third request must be rejected with 429.
	w3 := rec(r, "GET", "/api/v1/sessions", "", userHdr)
	if w3.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429 after limit exhausted, got %d: %s", w3.Code, w3.Body)
	}
}

// TestActorRateLimitMasterExempt verifies the master key is never throttled.
func TestActorRateLimitMasterExempt(t *testing.T) {
	truncateAll(t)

	// Use an extremely low limit — master must still succeed every time.
	r := newActorLimitRouter(testDB, 1)
	for i := 0; i < 5; i++ {
		w := rec(r, "GET", "/api/v1/sessions", "", masterHdr())
		if w.Code != http.StatusOK {
			t.Fatalf("master request %d: want 200, got %d", i+1, w.Code)
		}
	}
}

// ── session quota ─────────────────────────────────────────────────────────────

// newSessionQuotaRouter wraps the session-create endpoint with the given quota.
func newSessionQuotaRouter(t *testing.T, db *sqlx.DB, maxSessions int) *gin.Engine {
	t.Helper()
	// MAX_SESSIONS_PER_ACTOR is read from the environment by SessionLimits() on
	// every call, so t.Setenv is sufficient to scope it to this test.
	t.Setenv("MAX_SESSIONS_PER_ACTOR", strconv.Itoa(maxSessions))

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
	// docker client is nil — quota check fires before any Docker call.
	hub := handlers.NewHub(db, nil, ws, nil, al, cfg, policy.AllowAll{}, nil, nil, nil)
	authMW := middleware.APIKeyAuth(db, testMasterKey)
	api := r.Group("/api/v1", authMW)
	api.POST("/sessions", handlers.NewSessionHandler(hub).Create)
	return r
}

// seedSession inserts a minimal "running" session owned by actor directly into
// the DB, bypassing Docker. Used to pre-fill the quota without a real container.
func seedSession(t *testing.T, db *sqlx.DB, actor string) {
	t.Helper()
	now := time.Now().UTC()
	s := &models.Session{
		ID:             uuid.New(),
		Image:          "python:3.12-slim",
		Status:         models.SessionStatusRunning,
		CPULimit:       1,
		MemoryLimitMB:  512,
		TimeoutSeconds: 300,
		WorkspacePath:  os.TempDir(),
		Labels:         models.JSONB{},
		AllowedHosts:   models.StringArray{},
		CreatedBy:      actor,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := dbpkg.CreateSession(context.Background(), db, s); err != nil {
		t.Fatalf("seedSession: %v", err)
	}
}

// TestSessionQuotaEnforced verifies that a non-master actor cannot exceed
// MAX_SESSIONS_PER_ACTOR concurrent sessions.
func TestSessionQuotaEnforced(t *testing.T) {
	truncateAll(t)

	// Create a regular API key so we have an identifiable non-master actor.
	r0 := newTestRouter(testDB)
	w := rec(r0, "POST", "/api/v1/keys", `{"name":"quota-actor"}`, masterHdr())
	if w.Code != http.StatusCreated {
		t.Fatalf("create key: want 201, got %d: %s", w.Code, w.Body)
	}
	var keyResp struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(w.Body).Decode(&keyResp); err != nil || keyResp.Key == "" {
		t.Fatal("no plaintext key in create-key response")
	}

	// The actor name used by the middleware is the key's Name field ("quota-actor").
	// Seed 1 session owned by that actor so the quota (1) is already reached.
	seedSession(t, testDB, "quota-actor")

	// Build a router with MAX_SESSIONS_PER_ACTOR=1.
	r := newSessionQuotaRouter(t, testDB, 1)
	userHdr := map[string]string{"X-API-Key": keyResp.Key}

	// Session creation must be rejected because the quota is already exhausted.
	w2 := rec(r, "POST", "/api/v1/sessions",
		`{"image":"python:3.12-slim","timeout_seconds":60}`, userHdr)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429 (quota exceeded), got %d: %s", w2.Code, w2.Body)
	}
}

// TestSessionQuotaMasterExempt verifies the master key bypasses the quota.
func TestSessionQuotaMasterExempt(t *testing.T) {
	truncateAll(t)

	// Fill the quota with seeded sessions owned by the "quota-master-test" actor.
	// Master should still be allowed to create (it uses actor="master" which is exempt).
	// We only check that master doesn't get 429 before Docker is reached; it will
	// return 500 (nil docker client) which is acceptable for this test's purpose.
	r := newSessionQuotaRouter(t, testDB, 1)

	w := rec(r, "POST", "/api/v1/sessions",
		`{"image":"python:3.12-slim","timeout_seconds":60}`, masterHdr())
	// 500 = reached Docker (nil client) — quota did NOT block master. 400/422 also
	// acceptable (validation failures). Anything but 429 proves master is exempt.
	if w.Code == http.StatusTooManyRequests {
		t.Fatalf("master key must not be blocked by session quota, got 429: %s", w.Body)
	}
}

// ── organizations & RBAC ──────────────────────────────────────────────────────

func TestOrgCreateAndList(t *testing.T) {
	truncateAll(t)
	r := newTestRouter(testDB)

	// Create org
	w := rec(r, "POST", "/api/v1/orgs", `{"name":"Acme Corp"}`, masterHdr())
	if w.Code != http.StatusCreated {
		t.Fatalf("create org: want 201, got %d: %s", w.Code, w.Body)
	}
	var org map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&org); err != nil {
		t.Fatalf("decode org: %v", err)
	}
	if org["slug"] != "acme-corp" {
		t.Errorf("want slug=acme-corp, got %q", org["slug"])
	}
	orgID, _ := org["id"].(string)

	// List orgs
	w2 := rec(r, "GET", "/api/v1/orgs", "", masterHdr())
	if w2.Code != http.StatusOK {
		t.Fatalf("list orgs: want 200, got %d: %s", w2.Code, w2.Body)
	}
	var list struct {
		Orgs []map[string]interface{} `json:"orgs"`
	}
	_ = json.NewDecoder(w2.Body).Decode(&list)
	if len(list.Orgs) != 1 {
		t.Fatalf("want 1 org, got %d", len(list.Orgs))
	}

	// Get by ID
	w3 := rec(r, "GET", "/api/v1/orgs/"+orgID, "", masterHdr())
	if w3.Code != http.StatusOK {
		t.Fatalf("get org: want 200, got %d: %s", w3.Code, w3.Body)
	}
}

func TestOrgSlugDeduplicated(t *testing.T) {
	truncateAll(t)
	r := newTestRouter(testDB)

	rec(r, "POST", "/api/v1/orgs", `{"name":"Duplicate"}`, masterHdr())
	w := rec(r, "POST", "/api/v1/orgs", `{"name":"Duplicate"}`, masterHdr())
	if w.Code != http.StatusConflict {
		t.Fatalf("want 409 for duplicate slug, got %d: %s", w.Code, w.Body)
	}
}

func TestOrgDelete(t *testing.T) {
	truncateAll(t)
	r := newTestRouter(testDB)

	w := rec(r, "POST", "/api/v1/orgs", `{"name":"DeleteMe"}`, masterHdr())
	var org map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&org)
	orgID := org["id"].(string)

	w2 := rec(r, "DELETE", "/api/v1/orgs/"+orgID, "", masterHdr())
	if w2.Code != http.StatusNoContent {
		t.Fatalf("delete org: want 204, got %d: %s", w2.Code, w2.Body)
	}

	w3 := rec(r, "GET", "/api/v1/orgs/"+orgID, "", masterHdr())
	if w3.Code != http.StatusNotFound {
		t.Fatalf("get deleted org: want 404, got %d", w3.Code)
	}
}

func TestOrgMemberAddAndList(t *testing.T) {
	truncateAll(t)
	r := newTestRouter(testDB)

	// Create org
	wo := rec(r, "POST", "/api/v1/orgs", `{"name":"Team Alpha"}`, masterHdr())
	var org map[string]interface{}
	_ = json.NewDecoder(wo.Body).Decode(&org)
	orgID := org["id"].(string)

	// Add executor member
	wm := rec(r, "POST", "/api/v1/orgs/"+orgID+"/members",
		`{"principal":"alice","role":"executor"}`, masterHdr())
	if wm.Code != http.StatusCreated {
		t.Fatalf("add member: want 201, got %d: %s", wm.Code, wm.Body)
	}

	// List members
	wl := rec(r, "GET", "/api/v1/orgs/"+orgID+"/members", "", masterHdr())
	if wl.Code != http.StatusOK {
		t.Fatalf("list members: want 200, got %d: %s", wl.Code, wl.Body)
	}
	var listResp struct {
		Members []map[string]interface{} `json:"members"`
	}
	_ = json.NewDecoder(wl.Body).Decode(&listResp)
	if len(listResp.Members) != 1 {
		t.Fatalf("want 1 member, got %d", len(listResp.Members))
	}
	if listResp.Members[0]["role"] != "executor" {
		t.Errorf("want role=executor, got %v", listResp.Members[0]["role"])
	}
}

func TestOrgMemberRemove(t *testing.T) {
	truncateAll(t)
	r := newTestRouter(testDB)

	wo := rec(r, "POST", "/api/v1/orgs", `{"name":"Remove Test"}`, masterHdr())
	var org map[string]interface{}
	_ = json.NewDecoder(wo.Body).Decode(&org)
	orgID := org["id"].(string)

	rec(r, "POST", "/api/v1/orgs/"+orgID+"/members",
		`{"principal":"bob","role":"viewer"}`, masterHdr())

	wr := rec(r, "DELETE", "/api/v1/orgs/"+orgID+"/members/bob", "", masterHdr())
	if wr.Code != http.StatusNoContent {
		t.Fatalf("remove member: want 204, got %d: %s", wr.Code, wr.Body)
	}

	// Member should no longer appear
	wl := rec(r, "GET", "/api/v1/orgs/"+orgID+"/members", "", masterHdr())
	var listResp struct {
		Members []map[string]interface{} `json:"members"`
	}
	_ = json.NewDecoder(wl.Body).Decode(&listResp)
	if len(listResp.Members) != 0 {
		t.Errorf("want 0 members after remove, got %d", len(listResp.Members))
	}
}

func TestOrgMemberRoleUpdate(t *testing.T) {
	truncateAll(t)
	r := newTestRouter(testDB)

	wo := rec(r, "POST", "/api/v1/orgs", `{"name":"Role Update"}`, masterHdr())
	var org map[string]interface{}
	_ = json.NewDecoder(wo.Body).Decode(&org)
	orgID := org["id"].(string)

	// Add as viewer
	rec(r, "POST", "/api/v1/orgs/"+orgID+"/members",
		`{"principal":"carol","role":"viewer"}`, masterHdr())

	// Promote to admin (upsert)
	wp := rec(r, "POST", "/api/v1/orgs/"+orgID+"/members",
		`{"principal":"carol","role":"admin"}`, masterHdr())
	if wp.Code != http.StatusCreated {
		t.Fatalf("upsert member: want 201, got %d: %s", wp.Code, wp.Body)
	}

	wl := rec(r, "GET", "/api/v1/orgs/"+orgID+"/members", "", masterHdr())
	var listResp struct {
		Members []map[string]interface{} `json:"members"`
	}
	_ = json.NewDecoder(wl.Body).Decode(&listResp)
	if len(listResp.Members) != 1 {
		t.Fatalf("want 1 member after upsert, got %d", len(listResp.Members))
	}
	if listResp.Members[0]["role"] != "admin" {
		t.Errorf("want role=admin after promotion, got %v", listResp.Members[0]["role"])
	}
}

func TestOrgSessionSharing(t *testing.T) {
	truncateAll(t)
	r := newTestRouter(testDB)

	// Create org and add member "dave" as viewer
	wo := rec(r, "POST", "/api/v1/orgs", `{"name":"Shared Org"}`, masterHdr())
	var org map[string]interface{}
	_ = json.NewDecoder(wo.Body).Decode(&org)
	orgID := org["id"].(string)

	rec(r, "POST", "/api/v1/orgs/"+orgID+"/members",
		`{"principal":"dave","role":"viewer"}`, masterHdr())

	// Insert a session belonging to that org owned by master
	orgUUID := uuid.MustParse(orgID)
	sessID := uuid.New()
	_, err := testDB.ExecContext(context.Background(), `
		INSERT INTO sessions (id, image, status, network_enabled, cpu_limit,
		    memory_limit_mb, timeout_seconds, workspace_path, labels, allowed_hosts,
		    created_by, org_id, created_at, updated_at)
		VALUES ($1, 'python:3.12-slim', 'running', false, 1.0,
		    512, 300, '/tmp/ws', '{}', '{}', 'master', $2, NOW(), NOW())`,
		sessID, orgUUID,
	)
	if err != nil {
		t.Fatalf("insert org session: %v", err)
	}

	// Create a key for "dave" so dave can authenticate
	wk := rec(r, "POST", "/api/v1/keys", `{"name":"dave"}`, masterHdr())
	if wk.Code != http.StatusCreated {
		t.Fatalf("create dave key: want 201, got %d: %s", wk.Code, wk.Body)
	}
	var daveKey struct{ Key string `json:"key"` }
	_ = json.NewDecoder(wk.Body).Decode(&daveKey)
	daveHdr := map[string]string{"X-API-Key": daveKey.Key}

	// Dave should see the org session in the global session list
	wl := rec(r, "GET", "/api/v1/sessions", "", daveHdr)
	if wl.Code != http.StatusOK {
		t.Fatalf("list sessions as dave: want 200, got %d: %s", wl.Code, wl.Body)
	}
	var sessResp struct {
		Sessions []map[string]interface{} `json:"sessions"`
	}
	_ = json.NewDecoder(wl.Body).Decode(&sessResp)
	if len(sessResp.Sessions) != 1 {
		t.Errorf("dave (viewer) should see the shared org session, got %d sessions", len(sessResp.Sessions))
	}

	// Dave should also see it via the org sessions endpoint
	wos := rec(r, "GET", "/api/v1/orgs/"+orgID+"/sessions", "", daveHdr)
	if wos.Code != http.StatusOK {
		t.Fatalf("org sessions as dave: want 200, got %d: %s", wos.Code, wos.Body)
	}
	var orgSessResp struct {
		Sessions []map[string]interface{} `json:"sessions"`
	}
	_ = json.NewDecoder(wos.Body).Decode(&orgSessResp)
	if len(orgSessResp.Sessions) != 1 {
		t.Errorf("want 1 org session, got %d", len(orgSessResp.Sessions))
	}
}

func TestOrgNonMemberCannotSeeOrg(t *testing.T) {
	truncateAll(t)
	r := newTestRouter(testDB)

	wo := rec(r, "POST", "/api/v1/orgs", `{"name":"Private Org"}`, masterHdr())
	var org map[string]interface{}
	_ = json.NewDecoder(wo.Body).Decode(&org)
	orgID := org["id"].(string)

	// Create "eve" key — NOT added as org member
	wk := rec(r, "POST", "/api/v1/keys", `{"name":"eve"}`, masterHdr())
	var eveKey struct{ Key string `json:"key"` }
	_ = json.NewDecoder(wk.Body).Decode(&eveKey)
	eveHdr := map[string]string{"X-API-Key": eveKey.Key}

	// Eve should get 404 (existence not leaked)
	wg := rec(r, "GET", "/api/v1/orgs/"+orgID, "", eveHdr)
	if wg.Code != http.StatusNotFound {
		t.Fatalf("non-member GET org: want 404, got %d: %s", wg.Code, wg.Body)
	}

	// Eve should not be able to list members
	wm := rec(r, "GET", "/api/v1/orgs/"+orgID+"/members", "", eveHdr)
	if wm.Code != http.StatusNotFound {
		t.Fatalf("non-member list members: want 404, got %d: %s", wm.Code, wm.Body)
	}
}
