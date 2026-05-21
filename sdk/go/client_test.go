package vaultrun_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	vaultrun "github.com/nickvd7/vaultrun/sdk/go"
)

// ── Test helpers ──────────────────────────────────────────────────────────────

func newServer(t *testing.T, mux *http.ServeMux) (*httptest.Server, *vaultrun.Client) {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, vaultrun.New(srv.URL, "test-key")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func checkAPIKey(t *testing.T, r *http.Request) bool {
	t.Helper()
	if r.Header.Get("X-API-Key") != "test-key" {
		t.Errorf("missing or wrong X-API-Key header: %q", r.Header.Get("X-API-Key"))
		return false
	}
	return true
}

func fakeSession(id string) map[string]any {
	return map[string]any{
		"id":               id,
		"image":            "python:3.12-slim",
		"status":           "running",
		"network_enabled":  false,
		"cpu_limit":        1.0,
		"memory_limit_mb":  512,
		"timeout_seconds":  300,
		"labels":           map[string]string{},
		"allowed_hosts":    []string{},
		"created_at":       time.Now().UTC().Format(time.RFC3339),
	}
}

func fakeRun(id, sessionID string) map[string]any {
	exitCode := 0
	stdout := "hello world\n"
	durationMS := int64(123)
	return map[string]any{
		"id":               id,
		"session_id":       sessionID,
		"command":          "python",
		"args":             []string{"script.py"},
		"status":           "completed",
		"exit_code":        &exitCode,
		"stdout":           &stdout,
		"stderr":           nil,
		"duration_ms":      &durationMS,
		"timeout_seconds":  30,
		"output_truncated": false,
		"created_at":       time.Now().UTC().Format(time.RFC3339),
	}
}

func fakeFile(id, sessionID string) map[string]any {
	return map[string]any{
		"id":           id,
		"session_id":   sessionID,
		"path":         "/workspace/script.py",
		"size_bytes":   42,
		"content_type": "text/x-python",
		"created_at":   time.Now().UTC().Format(time.RFC3339),
	}
}

// ── Session tests ─────────────────────────────────────────────────────────────

func TestCreateSession(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		if !checkAPIKey(t, r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusCreated, fakeSession("sess-001"))
	})

	_, client := newServer(t, mux)

	sess, err := client.CreateSession(context.Background(), vaultrun.CreateSessionOptions{
		Image:         "python:3.12-slim",
		MemoryLimitMB: 512,
		CPULimit:      1.0,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if sess.ID != "sess-001" {
		t.Errorf("want sess-001, got %s", sess.ID)
	}
}

func TestGetSession(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/sessions/sess-abc", func(w http.ResponseWriter, r *http.Request) {
		checkAPIKey(t, r)
		writeJSON(w, http.StatusOK, fakeSession("sess-abc"))
	})

	_, client := newServer(t, mux)

	sess, err := client.GetSession(context.Background(), "sess-abc")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.ID != "sess-abc" {
		t.Errorf("want sess-abc, got %s", sess.ID)
	}
}

func TestListSessions(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		checkAPIKey(t, r)
		writeJSON(w, http.StatusOK, map[string]any{
			"sessions": []any{fakeSession("s1"), fakeSession("s2")},
			"pagination": map[string]any{
				"page": 1, "limit": 50, "offset": 0, "total": 2,
			},
		})
	})

	_, client := newServer(t, mux)

	sessions, err := client.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Errorf("want 2 sessions, got %d", len(sessions))
	}
}

func TestListSessionsWithLabelFilter(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		label := r.URL.Query().Get("label")
		if label != "env:prod" {
			t.Errorf("expected label=env:prod, got %q", label)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"sessions":   []any{fakeSession("s1")},
			"pagination": map[string]any{"page": 1, "limit": 50, "offset": 0, "total": 1},
		})
	})

	_, client := newServer(t, mux)

	sessions, err := client.ListSessions(context.Background(), vaultrun.ListSessionsOptions{
		LabelKey:   "env",
		LabelValue: "prod",
	})
	if err != nil {
		t.Fatalf("ListSessions with label: %v", err)
	}
	if len(sessions) != 1 {
		t.Errorf("want 1 session, got %d", len(sessions))
	}
}

func TestDeleteSession(t *testing.T) {
	called := false
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/sessions/sess-del", func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	})

	_, client := newServer(t, mux)

	if err := client.DeleteSession(context.Background(), "sess-del"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if !called {
		t.Error("DELETE handler was not called")
	}
}

func TestUpdateSessionLabels(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/sessions/sess-lbl/labels", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("expected PATCH, got %s", r.Method)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		labels, _ := body["labels"].(map[string]any)
		if labels["env"] != "prod" {
			t.Errorf("expected env=prod in labels, got %v", labels)
		}
		writeJSON(w, http.StatusOK, map[string]any{"labels": labels})
	})

	_, client := newServer(t, mux)

	if err := client.UpdateSessionLabels(context.Background(), "sess-lbl", map[string]string{"env": "prod"}); err != nil {
		t.Fatalf("UpdateSessionLabels: %v", err)
	}
}

// ── Run tests ─────────────────────────────────────────────────────────────────

func TestRun(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/sessions/sess-1/run", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		writeJSON(w, http.StatusOK, fakeRun("run-1", "sess-1"))
	})

	_, client := newServer(t, mux)

	run, err := client.Run(context.Background(), "sess-1", vaultrun.RunOptions{
		Command: "python",
		Args:    []string{"script.py"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.ID != "run-1" {
		t.Errorf("want run-1, got %s", run.ID)
	}
	if run.Status != "completed" {
		t.Errorf("want completed, got %s", run.Status)
	}
}

func TestRunAsync(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/sessions/sess-2/run/async", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if cb, _ := body["callback_url"].(string); cb != "http://example.com/cb" {
			t.Errorf("expected callback_url, got %q", cb)
		}
		writeJSON(w, http.StatusAccepted, map[string]any{
			"run_id":  "run-async-1",
			"status":  "pending",
			"message": "run enqueued",
		})
	})

	_, client := newServer(t, mux)

	result, err := client.RunAsync(context.Background(), "sess-2", vaultrun.AsyncRunOptions{
		RunOptions:  vaultrun.RunOptions{Command: "python", Args: []string{"long.py"}},
		CallbackURL: "http://example.com/cb",
	})
	if err != nil {
		t.Fatalf("RunAsync: %v", err)
	}
	if result.RunID != "run-async-1" {
		t.Errorf("want run-async-1, got %s", result.RunID)
	}
	if result.Status != "pending" {
		t.Errorf("want pending, got %s", result.Status)
	}
}

func TestGetRun(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/runs/run-xyz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, fakeRun("run-xyz", "sess-3"))
	})

	_, client := newServer(t, mux)

	run, err := client.GetRun(context.Background(), "run-xyz")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if run.ID != "run-xyz" {
		t.Errorf("want run-xyz, got %s", run.ID)
	}
}

func TestListRuns(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/sessions/sess-4/runs", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"runs":       []any{fakeRun("r1", "sess-4"), fakeRun("r2", "sess-4")},
			"pagination": map[string]any{"page": 1, "limit": 50, "offset": 0, "total": 2},
		})
	})

	_, client := newServer(t, mux)

	runs, err := client.ListRuns(context.Background(), "sess-4")
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 2 {
		t.Errorf("want 2 runs, got %d", len(runs))
	}
}

// ── File tests ────────────────────────────────────────────────────────────────

func TestUploadFile(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/sessions/sess-5/files", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
		}
		if path := r.FormValue("path"); path != "script.py" {
			t.Errorf("expected path=script.py, got %q", path)
		}
		_, fh, err := r.FormFile("file")
		if err != nil || fh == nil {
			t.Errorf("no file field in upload: %v", err)
		}
		writeJSON(w, http.StatusOK, fakeFile("file-1", "sess-5"))
	})

	_, client := newServer(t, mux)

	f, err := client.UploadFile(context.Background(), "sess-5", "script.py",
		strings.NewReader("print('hello')"))
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	if f.ID != "file-1" {
		t.Errorf("want file-1, got %s", f.ID)
	}
}

func TestDownloadFile(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/sessions/sess-6/files/script.py", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/x-python")
		fmt.Fprint(w, "print('hello')")
	})

	_, client := newServer(t, mux)

	rc, err := client.DownloadFile(context.Background(), "sess-6", "script.py")
	if err != nil {
		t.Fatalf("DownloadFile: %v", err)
	}
	defer rc.Close()

	content, _ := io.ReadAll(rc)
	if string(content) != "print('hello')" {
		t.Errorf("unexpected content: %q", string(content))
	}
}

func TestDownloadWorkspace(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/sessions/sess-7/workspace.zip", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		fmt.Fprint(w, "PKzipdata")
	})

	_, client := newServer(t, mux)

	rc, err := client.DownloadWorkspace(context.Background(), "sess-7")
	if err != nil {
		t.Fatalf("DownloadWorkspace: %v", err)
	}
	defer rc.Close()

	data, _ := io.ReadAll(rc)
	if !strings.HasPrefix(string(data), "PK") {
		t.Errorf("expected ZIP prefix, got %q", string(data))
	}
}

func TestListFiles(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/sessions/sess-8/files", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"files": []any{fakeFile("f1", "sess-8")},
			"pagination": map[string]any{"page": 1, "limit": 50, "offset": 0, "total": 1},
		})
	})

	_, client := newServer(t, mux)

	files, err := client.ListFiles(context.Background(), "sess-8")
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 1 {
		t.Errorf("want 1 file, got %d", len(files))
	}
}

func TestDeleteFile(t *testing.T) {
	called := false
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/sessions/sess-9/files/output.txt", func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	})

	_, client := newServer(t, mux)

	if err := client.DeleteFile(context.Background(), "sess-9", "output.txt"); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if !called {
		t.Error("DELETE handler not called")
	}
}

// ── API key tests ─────────────────────────────────────────────────────────────

func TestListKeys(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/keys", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"api_keys": []any{
				map[string]any{
					"id": "key-1", "name": "ci", "prefix": "vr_abc",
					"active": true, "created_at": time.Now().UTC().Format(time.RFC3339),
				},
			},
			"pagination": map[string]any{"page": 1, "limit": 50, "offset": 0, "total": 1},
		})
	})

	_, client := newServer(t, mux)

	keys, err := client.ListKeys(context.Background())
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	if len(keys) != 1 {
		t.Errorf("want 1 key, got %d", len(keys))
	}
	if keys[0].Name != "ci" {
		t.Errorf("want name=ci, got %s", keys[0].Name)
	}
}

func TestCreateKey(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/keys", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["name"] != "deploy" {
			t.Errorf("want name=deploy, got %v", body["name"])
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"id": "key-2", "name": "deploy", "prefix": "vr_xyz",
			"active": true, "created_at": time.Now().UTC().Format(time.RFC3339),
			"key": "vr_xyz_fulllongkey",
		})
	})

	_, client := newServer(t, mux)

	key, err := client.CreateKey(context.Background(), vaultrun.CreateKeyOptions{Name: "deploy"})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}
	if key.Key != "vr_xyz_fulllongkey" {
		t.Errorf("want full key in response, got %q", key.Key)
	}
}

func TestRevokeKey(t *testing.T) {
	called := false
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/keys/key-99", func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})

	_, client := newServer(t, mux)

	if err := client.RevokeKey(context.Background(), "key-99"); err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}
	if !called {
		t.Error("DELETE handler not called")
	}
}

// ── Stream test ───────────────────────────────────────────────────────────────

func TestStream(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/sessions/sess-s/run/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("ResponseWriter does not implement Flusher")
			return
		}
		events := []string{
			`{"type":"stdout","data":"hello\n"}`,
			`{"type":"stderr","data":"warning\n"}`,
			`{"type":"done","run_id":"run-s1","status":"completed","exit_code":0,"duration_ms":42}`,
		}
		for _, e := range events {
			fmt.Fprintf(w, "data: %s\n\n", e)
			flusher.Flush()
		}
	})

	_, client := newServer(t, mux)

	var stdout, stderr strings.Builder
	ev, err := client.Stream(context.Background(), "sess-s", vaultrun.RunOptions{Command: "echo"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if ev.Type != "done" {
		t.Errorf("expected done event, got %q", ev.Type)
	}
	if ev.Status != "completed" {
		t.Errorf("expected completed status, got %q", ev.Status)
	}
	if stdout.String() != "hello\n" {
		t.Errorf("unexpected stdout: %q", stdout.String())
	}
	if stderr.String() != "warning\n" {
		t.Errorf("unexpected stderr: %q", stderr.String())
	}
}

// ── Error handling test ───────────────────────────────────────────────────────

func TestAPIError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/sessions/not-found", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "session not found"})
	})

	_, client := newServer(t, mux)

	_, err := client.GetSession(context.Background(), "not-found")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "session not found") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestAPIKeyHeader(t *testing.T) {
	received := ""
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Get("X-API-Key")
		writeJSON(w, http.StatusOK, map[string]any{
			"sessions":   []any{},
			"pagination": map[string]any{"page": 1, "limit": 50, "offset": 0, "total": 0},
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := vaultrun.New(srv.URL, "my-secret-key")

	_, _ = client.ListSessions(context.Background())
	if received != "my-secret-key" {
		t.Errorf("expected X-API-Key=my-secret-key, got %q", received)
	}
}
