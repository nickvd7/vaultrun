//go:build e2e

package main

// Full end-to-end smoke test: spins up a real Postgres, connects to the local
// Docker daemon, builds the full router (same as main.go), and exercises the
// entire happy path:
//
//	create API key → create session → upload file → run script →
//	list runs → verify audit trail → delete session
//
// Requirements:
//   - Docker daemon reachable (unix:///var/run/docker.sock or DOCKER_HOST)
//   - Either INTEGRATION_DSN set, or Docker available for testcontainers Postgres
//
// Run locally:
//
//	go test -tags e2e ./cmd/api/ -v -timeout 300s

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/jmoiron/sqlx"

	"github.com/nickvd7/vaultrun/internal/audit"
	"github.com/nickvd7/vaultrun/internal/config"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	dockerpkg "github.com/nickvd7/vaultrun/internal/docker"
	"github.com/nickvd7/vaultrun/internal/jobqueue"
	"github.com/nickvd7/vaultrun/internal/policy"
	"github.com/nickvd7/vaultrun/internal/runner"
	"github.com/nickvd7/vaultrun/internal/workspace"
)

const (
	e2eMasterKey = "e2e-smoke-master-key"
	e2eImage     = "python:3.12-slim"
)

// TestE2ESmoke is the end-to-end smoke test covering the full VaultRun flow.
func TestE2ESmoke(t *testing.T) {
	ctx := context.Background()

	// ── 0. Docker client ─────────────────────────────────────────────────────
	dockerClient, err := dockerpkg.New()
	if err != nil {
		t.Skipf("Docker not available — skipping E2E: %v", err)
	}
	// Verify Docker daemon is actually reachable.
	if _, err := dockerClient.Inner().Ping(ctx); err != nil {
		t.Skipf("Docker ping failed — skipping E2E: %v", err)
	}

	// Ensure the sandbox image is locally available. If CI has already pre-pulled
	// it (via the "Pull sandbox image" workflow step), we skip the registry pull
	// entirely — Docker Hub enforces a 100-pull/6 h rate limit for unauthenticated
	// runners, and unconditionally calling PullImage burns that quota even when
	// the image is already cached, which is the primary cause of intermittent
	// CI failures.
	t.Log("checking sandbox image availability…")
	imageAvailable, err := dockerClient.ImageExists(ctx, e2eImage)
	if err != nil {
		t.Logf("image existence check error (%v); will attempt pull", err)
		imageAvailable = false
	}
	if !imageAvailable {
		t.Logf("image %s not found locally — pulling from registry…", e2eImage)
		pullCtx, pullCancel := context.WithTimeout(ctx, 5*time.Minute)
		defer pullCancel()
		if err := dockerClient.PullImage(pullCtx, e2eImage); err != nil {
			t.Skipf("cannot pull %s: %v", e2eImage, err)
		}
	} else {
		t.Logf("image %s found in local cache — skipping registry pull", e2eImage)
	}

	// ── 1. Database ───────────────────────────────────────────────────────────
	db := setupE2EDB(t, ctx)

	// ── 2. Build the full router ──────────────────────────────────────────────
	wsDir := t.TempDir()
	cfg := &config.Config{
		Auth: config.AuthConfig{MasterKey: e2eMasterKey},
		Docker: config.DockerConfig{
			DefaultImage:    e2eImage,
			ContainerPrefix: "vaultrun-e2e",
			NetworkName:     "none",
		},
		Server:    config.ServerConfig{RateLimit: 0},
		Workspace: config.WorkspaceConfig{BaseDir: wsDir, MaxFileMB: 100},
	}

	al := audit.New(db, "")
	ws := workspace.New(wsDir)
	rnr := runner.New(db, dockerClient, al, policy.AllowAll{})
	queue := jobqueue.New(rnr, 2, 64, "")
	r := newRouter(cfg, db, dockerClient, ws, rnr, al, policy.AllowAll{}, queue, nil, nil, nil, enterpriseHooks{})

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	c := &smokeClient{base: srv.URL, key: e2eMasterKey, t: t}

	// ── 3. Health check ───────────────────────────────────────────────────────
	t.Run("health", func(t *testing.T) {
		resp := c.getMap("/health")
		if resp["status"] != "ok" {
			t.Fatalf("health: want status=ok, got %v", resp["status"])
		}
	})

	// ── 4. Create API key — verify it can authenticate ────────────────────────
	var plainKey string
	t.Run("create_and_use_api_key", func(t *testing.T) {
		resp := c.postMap("/api/v1/keys", map[string]any{"name": "e2e-key"})
		plainKey, _ = resp["key"].(string)
		if plainKey == "" {
			t.Fatal("create_key: no plaintext key in response")
		}
		// Authenticate with the new user key
		userClient := &smokeClient{base: srv.URL, key: plainKey, t: t}
		list := userClient.getMap("/api/v1/sessions")
		if list["sessions"] == nil {
			t.Fatal("user key: failed to list sessions")
		}
	})

	// ── 5. Create session ─────────────────────────────────────────────────────
	var sessionID string
	t.Run("create_session", func(t *testing.T) {
		resp := c.postMap("/api/v1/sessions", map[string]any{
			"image":           e2eImage,
			"timeout_seconds": 60,
		})
		sessionID, _ = resp["id"].(string)
		if sessionID == "" {
			t.Fatal("create_session: no id in response")
		}
		status, _ := resp["status"].(string)
		if status != "running" {
			t.Fatalf("create_session: want status=running, got %q", status)
		}
		t.Logf("session %s running", sessionID)
	})

	// Register cleanup on the PARENT test so the session stays alive for all
	// subsequent subtests. t.Cleanup on the subtest's t fires the moment that
	// subtest returns — before upload_file, execute_run, etc. ever run.
	t.Cleanup(func() {
		if sessionID != "" {
			c.deleteRaw("/api/v1/sessions/" + sessionID)
		}
	})

	if t.Failed() || sessionID == "" {
		t.Fatal("session setup failed — aborting E2E")
	}

	// ── 6. Upload a Python script ─────────────────────────────────────────────
	t.Run("upload_file", func(t *testing.T) {
		script := `import sys
print("hello from vaultrun e2e")
print("args:", sys.argv[1:])
sys.exit(0)
`
		// Upload to "hello.py" (relative to workspace root).
		// SafePath joins this with the session dir, so the file lands at
		// {session_dir}/hello.py on the host, which the Docker bind-mount
		// exposes as /workspace/hello.py inside the container — exactly the
		// path the execute_run step passes to Python.
		file := c.uploadFile(sessionID, "hello.py", []byte(script))
		if file["path"] == nil {
			t.Fatal("upload_file: no path in response")
		}
		t.Logf("uploaded %v (%v bytes)", file["path"], file["size_bytes"])
	})

	// ── 7. Execute the script ─────────────────────────────────────────────────
	var runID string
	t.Run("execute_run", func(t *testing.T) {
		resp := c.postMap("/api/v1/sessions/"+sessionID+"/run", map[string]any{
			"command":         "python",
			"args":            []string{"/workspace/hello.py", "arg1", "arg2"},
			"timeout_seconds": 20,
		})

		runID, _ = resp["id"].(string)
		status, _ := resp["status"].(string)
		stdout, _ := resp["stdout"].(string)
		exitCode := resp["exit_code"]

		if status != "completed" {
			t.Fatalf("execute_run: want status=completed, got %q (stderr: %v)", status, resp["stderr"])
		}
		if ec, ok := exitCode.(float64); !ok || ec != 0 {
			t.Fatalf("execute_run: want exit_code=0, got %v", exitCode)
		}
		if !strings.Contains(stdout, "hello from vaultrun e2e") {
			t.Fatalf("execute_run: unexpected stdout: %q", stdout)
		}
		if !strings.Contains(stdout, "arg1") {
			t.Fatalf("execute_run: args not passed to script, stdout: %q", stdout)
		}
		t.Logf("run %s completed in %.0f ms — stdout: %q", runID, resp["duration_ms"], stdout)
	})

	// ── 8. List runs for session ──────────────────────────────────────────────
	t.Run("list_runs", func(t *testing.T) {
		resp := c.getMap("/api/v1/sessions/" + sessionID + "/runs")
		runs, _ := resp["runs"].([]any)
		if len(runs) < 1 {
			t.Fatalf("list_runs: want ≥1 run, got %d", len(runs))
		}
		first := runs[0].(map[string]any)
		if first["id"] != runID {
			t.Errorf("list_runs: first run id = %v, want %v", first["id"], runID)
		}
	})

	// ── 9. Get individual run ─────────────────────────────────────────────────
	t.Run("get_run", func(t *testing.T) {
		if runID == "" {
			t.Skip("no run ID")
		}
		resp := c.getMap("/api/v1/runs/" + runID)
		if resp["id"] != runID {
			t.Fatalf("get_run: id mismatch: %v", resp["id"])
		}
		if resp["status"] != "completed" {
			t.Fatalf("get_run: status = %v", resp["status"])
		}
	})

	// ── 10. Audit trail ───────────────────────────────────────────────────────
	t.Run("audit_trail", func(t *testing.T) {
		resp := c.getMap("/api/v1/audit?session_id=" + sessionID)
		logs, _ := resp["audit_logs"].([]any)

		seen := map[string]bool{}
		for _, l := range logs {
			entry := l.(map[string]any)
			seen[entry["action"].(string)] = true
		}

		required := []string{
			"session.created",
			"command.started",
		}
		// command.finished is emitted on success; command.failed on non-zero exit
		// or error — either counts as evidence the run completed its lifecycle.
		if !seen["command.finished"] && !seen["command.failed"] {
			t.Errorf("audit_trail: expected command.finished or command.failed (got: %v)", seen)
		}
		for _, action := range required {
			if !seen[action] {
				t.Errorf("audit_trail: missing action %q (got: %v)", action, seen)
			}
		}
		t.Logf("audit actions: %v", seen)
	})

	// ── 11. Policy dry-run eval ───────────────────────────────────────────────
	t.Run("policy_eval_allowall", func(t *testing.T) {
		resp := c.postMap("/api/v1/policy/eval", map[string]any{
			"type":    "command",
			"command": "python",
			"args":    []string{"script.py"},
		})
		allowed, _ := resp["allowed"].(bool)
		if !allowed {
			t.Fatalf("policy_eval: expected allowed=true with AllowAll hook, got %v", resp)
		}
	})

	// ── 12. List files — uploaded file is present ─────────────────────────────
	t.Run("list_files", func(t *testing.T) {
		resp := c.getMap("/api/v1/sessions/" + sessionID + "/files")
		files, _ := resp["files"].([]any)
		if len(files) < 1 {
			t.Fatalf("list_files: want ≥1 file (at least hello.py), got %d", len(files))
		}
		// The manually-uploaded script must be present regardless of artifact detection.
		found := false
		for _, f := range files {
			fm := f.(map[string]any)
			if path, _ := fm["path"].(string); path == "/hello.py" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("list_files: /hello.py not found in %v", files)
		}
	})

	// ── 13. Artifact detection — run writes a file, check it appears ──────────
	t.Run("artifact_detection", func(t *testing.T) {
		// Run a command that creates a new file in the workspace.
		writeCmd := c.postMap("/api/v1/sessions/"+sessionID+"/run", map[string]any{
			"command":         "python",
			"args":            []string{"-c", "open('/workspace/artifact.txt','w').write('detected')"},
			"timeout_seconds": 10,
		})
		if status, _ := writeCmd["status"].(string); status != "completed" {
			t.Fatalf("artifact run: want completed, got %q (stderr: %v)", status, writeCmd["stderr"])
		}

		// The file should now appear in the session's file list without an
		// explicit upload — detectArtifacts registers it automatically.
		resp := c.getMap("/api/v1/sessions/" + sessionID + "/files")
		files, _ := resp["files"].([]any)

		found := false
		for _, f := range files {
			fm := f.(map[string]any)
			if path, _ := fm["path"].(string); path == "/artifact.txt" {
				found = true
				t.Logf("artifact.txt detected: size=%v content_type=%v", fm["size_bytes"], fm["content_type"])
				break
			}
		}
		if !found {
			t.Errorf("artifact_detection: /artifact.txt not found in file list after run; files: %v", files)
		}
	})
}

// ── helpers ────────────────────────────────────────────────────────────────

// setupE2EDB returns a connected, migrated *sqlx.DB; it terminates the
// testcontainers Postgres on test cleanup if one was started.
func setupE2EDB(t *testing.T, ctx context.Context) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("INTEGRATION_DSN")

	if dsn == "" {
		pgC, err := postgres.Run(ctx,
			"postgres:16-alpine",
			postgres.WithDatabase("vaultrun_e2e"),
			postgres.WithUsername("vaultrun"),
			postgres.WithPassword("testpassword"),
			tc.WithWaitStrategy(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).
					WithStartupTimeout(60*time.Second),
			),
		)
		if err != nil {
			t.Skipf("SKIP: cannot start Postgres container (no Docker?): %v", err)
		}
		t.Cleanup(func() { _ = pgC.Terminate(ctx) })

		dsn, err = pgC.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			t.Fatalf("e2e db: connection string: %v", err)
		}
	}

	db, err := dbpkg.Connect(config.DatabaseConfig{
		DSN:             dsn,
		MaxOpenConns:    5,
		MaxIdleConns:    2,
		ConnMaxLifetime: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("e2e db: connect: %v", err)
	}

	migrationsPath := os.Getenv("MIGRATIONS_PATH")
	if migrationsPath == "" {
		migrationsPath = "../../migrations"
	}
	if err := dbpkg.RunMigrations(db, migrationsPath); err != nil {
		t.Fatalf("e2e db: migrations: %v", err)
	}
	return db
}

// smokeClient is a thin HTTP client for smoke tests.
type smokeClient struct {
	base string
	key  string
	t    *testing.T
}

func (c *smokeClient) getMap(path string) map[string]any {
	c.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, c.base+path, nil)
	req.Header.Set("X-API-Key", c.key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		c.t.Fatalf("GET %s: status %d: %s", path, resp.StatusCode, body)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		c.t.Fatalf("GET %s: decode: %v (body: %s)", path, err, body)
	}
	return m
}

func (c *smokeClient) postMap(path string, body map[string]any) map[string]any {
	c.t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, c.base+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", c.key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		c.t.Fatalf("POST %s: status %d: %s", path, resp.StatusCode, raw)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		c.t.Fatalf("POST %s: decode: %v (body: %s)", path, err, raw)
	}
	return m
}

func (c *smokeClient) deleteRaw(path string) {
	req, _ := http.NewRequest(http.MethodDelete, c.base+path, nil)
	req.Header.Set("X-API-Key", c.key)
	resp, _ := http.DefaultClient.Do(req)
	if resp != nil {
		resp.Body.Close()
	}
}

// uploadFile uploads content as multipart/form-data to the files endpoint.
func (c *smokeClient) uploadFile(sessionID, remotePath string, content []byte) map[string]any {
	c.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("path", remotePath)
	fw, err := mw.CreateFormFile("file", filepath.Base(remotePath))
	if err != nil {
		c.t.Fatalf("upload: create form file: %v", err)
	}
	if _, err := fw.Write(content); err != nil {
		c.t.Fatalf("upload: write content: %v", err)
	}
	mw.Close()

	req, _ := http.NewRequest(http.MethodPost,
		fmt.Sprintf("%s/api/v1/sessions/%s/files", c.base, sessionID),
		&buf,
	)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-API-Key", c.key)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("upload: request: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		c.t.Fatalf("upload: status %d: %s", resp.StatusCode, raw)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		c.t.Fatalf("upload: decode: %v (body: %s)", err, raw)
	}
	return m
}
