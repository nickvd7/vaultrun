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
	"errors"
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
	"github.com/nickvd7/vaultrun/internal/cost"
	dbpkg "github.com/nickvd7/vaultrun/internal/db"
	dockerpkg "github.com/nickvd7/vaultrun/internal/docker"
	"github.com/nickvd7/vaultrun/internal/evidence"
	"github.com/nickvd7/vaultrun/internal/jobqueue"
	"github.com/nickvd7/vaultrun/internal/missions"
	"github.com/nickvd7/vaultrun/internal/policy"
	"github.com/nickvd7/vaultrun/internal/replay"
	"github.com/nickvd7/vaultrun/internal/runner"
	"github.com/nickvd7/vaultrun/internal/templates"
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
	// Same startup check as main.go; it also learns whether a GPU can be
	// passed through under DOCKER_RUNTIME.
	if err := dockerClient.CheckRuntime(ctx); err != nil {
		t.Fatalf("DOCKER_RUNTIME: %v", err)
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
	evidenceKey, _, err := evidence.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Auth: config.AuthConfig{MasterKey: e2eMasterKey},
		Docker: config.DockerConfig{
			DefaultImage:    e2eImage,
			ContainerPrefix: "vaultrun-e2e",
			NetworkName:     "none",
			// No session in this test asks for a GPU except
			// gpu_rejected_without_nvproxy, which must be refused before any
			// container is created.
			GPUDevices: "all",
		},
		Server:        config.ServerConfig{RateLimit: 0},
		Workspace:     config.WorkspaceConfig{BaseDir: wsDir, MaxFileMB: 100},
		Observability: config.ObservabilityConfig{EvidenceSigningKey: evidenceKey},
	}

	al := audit.New(db, "")
	ws := workspace.New(wsDir)
	rnr := runner.New(db, dockerClient, al, policy.AllowAll{})
	queue := jobqueue.New(rnr, 2, 64, "")

	// The v0.3.x feature managers are wired in so their routes are reachable.
	// collab is left nil: it needs Redis, which this smoke test does not start,
	// and newRouter skips those routes when it is absent.
	costTracker := cost.New(db, []byte(e2eMasterKey))
	templatesManager := templates.New(db)
	missionsManager := missions.New(db)
	replayMgr := replay.New(db, ws, []byte(e2eMasterKey))

	r := newRouter(cfg, db, dockerClient, ws, rnr, al, policy.AllowAll{}, queue,
		nil, nil, nil, costTracker, templatesManager, missionsManager, nil, nil, replayMgr, enterpriseHooks{})

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

	// Under gVisor without --nvproxy a GPU session cannot start; the API must
	// refuse it with a reason instead of failing container creation.
	t.Run("gpu_rejected_without_nvproxy", func(t *testing.T) {
		if dockerClient.GPUSupported() == nil {
			t.Skip("runtime supports GPU passthrough (or is not gVisor)")
		}
		code, body := c.postStatus("/api/v1/sessions", map[string]any{
			"image":       e2eImage,
			"gpu_enabled": true,
		})
		if code != http.StatusNotImplemented || !strings.Contains(body, "nvproxy") {
			t.Fatalf("gpu session: want 501 mentioning nvproxy, got %d: %s", code, body)
		}
	})

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
			// With DOCKER_RUNTIME set (CI runs this test under gVisor), the
			// signed session.created entry names the runtime.
			if entry["action"] == "session.created" {
				meta, _ := entry["metadata"].(map[string]any)
				if want := dockerClient.Runtime(); want != "" && meta["runtime"] != want {
					t.Errorf("audit_trail: session.created runtime = %v, want %q", meta["runtime"], want)
				}
			}
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

	// ── 14. Evidence pack — export, verify offline, check contents ────────────
	t.Run("evidence_pack", func(t *testing.T) {
		keyResp := c.getMap("/api/v1/evidence/public-key")
		pemStr, _ := keyResp["public_key_pem"].(string)
		pub, err := evidence.ParsePublicKeyPEM([]byte(pemStr))
		if err != nil {
			t.Fatalf("public key: %v", err)
		}
		raw := c.getRaw("/api/v1/sessions/" + sessionID + "/evidence")
		pack, err := evidence.Verify(raw, pub)
		if err != nil {
			t.Fatalf("verify exported pack: %v", err)
		}

		if pack.Session.ID != sessionID || pack.Session.Image != e2eImage || pack.Session.Runtime != dockerClient.Runtime() {
			t.Errorf("session = %+v", pack.Session)
		}
		if pack.Audit.Scope != evidence.AuditScopeAll || pack.ExportedBy != "master" {
			t.Errorf("master export: scope=%q exported_by=%q", pack.Audit.Scope, pack.ExportedBy)
		}
		var helloRun *evidence.Run
		for i, r := range pack.Runs {
			if len(r.Args) > 0 && r.Args[0] == "/workspace/hello.py" {
				helloRun = &pack.Runs[i]
			}
		}
		if helloRun == nil || helloRun.ExitCode == nil || *helloRun.ExitCode != 0 ||
			!strings.Contains(helloRun.Stdout, "hello from vaultrun e2e") || helloRun.StdoutSHA256 != evidence.SHA256Hex(helloRun.Stdout) {
			t.Errorf("hello.py run missing or wrong: %+v", helloRun)
		}
		for i := 1; i < len(pack.Runs); i++ {
			if pack.Runs[i].CreatedAt.Before(pack.Runs[i-1].CreatedAt) {
				t.Errorf("runs not oldest first")
			}
		}
		files := map[string]string{}
		for _, f := range pack.Files {
			files[f.Path] = f.SHA256
		}
		if files["/artifact.txt"] != evidence.SHA256Hex("detected") || files["/hello.py"] == "" {
			t.Errorf("files = %v", files)
		}
		actions := map[string]bool{}
		for _, e := range pack.Audit.Entries {
			actions[e.Action] = true
		}
		for _, a := range []string{"session.created", "file.uploaded", "command.started", "command.finished"} {
			if !actions[a] {
				t.Errorf("audit entries lack %s: %v", a, actions)
			}
		}

		// One changed byte anywhere in the pack breaks the signature.
		var env evidence.Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatal(err)
		}
		env.Pack = bytes.Replace(env.Pack, []byte("hello from vaultrun e2e"), []byte("hello from vaultrun E2E"), 1)
		tampered, _ := json.Marshal(env)
		if _, err := evidence.Verify(tampered, pub); !errors.Is(err, evidence.ErrBadSignature) {
			t.Errorf("tampered pack: want ErrBadSignature, got %v", err)
		}

		// The export itself is audited.
		audit := c.getMap("/api/v1/audit?session_id=" + sessionID + "&action=evidence.exported")
		if logs, _ := audit["audit_logs"].([]any); len(logs) != 1 {
			t.Errorf("want one evidence.exported entry, got %d", len(logs))
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

// postStatus posts body and returns the status code and raw response, for
// requests that are expected to fail.
func (c *smokeClient) postStatus(path string, body map[string]any) (int, string) {
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
	return resp.StatusCode, string(raw)
}

// getRaw returns the body of a successful GET.
func (c *smokeClient) getRaw(path string) []byte {
	c.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, c.base+path, nil)
	req.Header.Set("X-API-Key", c.key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		c.t.Fatalf("GET %s: status %d: %s", path, resp.StatusCode, body)
	}
	return body
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
