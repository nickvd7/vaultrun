package commands

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nickvd7/vaultrun/internal/evidence"
	"github.com/nickvd7/vaultrun/internal/evidence/evidencetest"
)

// runEvidence runs `vaultrun evidence <args>` and returns its output.
func runEvidence(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newEvidenceCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// writePack signs a small pack and writes it and the public key to dir.
func writePack(t *testing.T, dir string) (packFile, keyFile string) {
	t.Helper()
	seed, pubPEM, err := evidence.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	s, _ := evidence.ParseSigningKey(seed)
	exit := 0
	env, err := s.Sign(&evidence.Pack{
		Schema:  evidence.PackSchema,
		ID:      "pack-1",
		Session: evidence.Session{ID: "sess-1", Name: "invoice-agent", Image: "python:3.12-slim", Runtime: "runsc"},
		Runs:    []evidence.Run{evidence.NewRun(evidence.Run{ID: "r1", Command: "python", Args: []string{"report.py"}, ExitCode: &exit}, "ok\n", "")},
		Files:   []evidence.File{},
		Audit:   evidence.AuditRecord{Scope: evidence.AuditScopeAll, Signed: true, Entries: []evidence.AuditEntry{{ID: "a1", Action: "session.created", SigStatus: "valid"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(env)
	packFile, keyFile = filepath.Join(dir, "pack.json"), filepath.Join(dir, "key.pub")
	if err := os.WriteFile(packFile, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pubPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	return packFile, keyFile
}

func TestEvidenceVerifyCommand(t *testing.T) {
	dir := t.TempDir()
	pack, key := writePack(t, dir)

	out, err := runEvidence(t, "verify", pack, "--key", key)
	if err != nil {
		t.Fatalf("verify: %v\n%s", err, out)
	}
	for _, want := range []string{"VALID signature", `"invoice-agent"`, "runtime runsc", "exit 0   python report.py", "no external timestamp"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if _, err := runEvidence(t, "verify", pack, "--key", key, "--strict"); err != nil {
		t.Errorf("--strict on a pack with notes only: %v", err)
	}

	// Change one character of the signed pack.
	var env map[string]any
	data, _ := os.ReadFile(pack)
	_ = json.Unmarshal(data, &env)
	raw, _ := base64.StdEncoding.DecodeString(env["pack"].(string))
	env["pack"] = base64.StdEncoding.EncodeToString(bytes.Replace(raw, []byte("report.py"), []byte("rep0rt.py"), 1))
	tampered := filepath.Join(dir, "tampered.json")
	b, _ := json.Marshal(env)
	_ = os.WriteFile(tampered, b, 0o600)
	if _, err := runEvidence(t, "verify", tampered, "--key", key); err == nil || !strings.Contains(err.Error(), "NOT VALID") {
		t.Errorf("tampered pack: want NOT VALID, got %v", err)
	}

	_, otherKey := writePack(t, t.TempDir())
	if _, err := runEvidence(t, "verify", pack, "--key", otherKey); err == nil || !strings.Contains(err.Error(), "NOT VALID") {
		t.Errorf("other key: want NOT VALID, got %v", err)
	}
	if _, err := runEvidence(t, "verify", pack); err == nil {
		t.Error("verify without --key succeeded")
	}
}

func TestEvidenceTimestampCommand(t *testing.T) {
	tsa := evidencetest.NewTSA(t, evidencetest.Options{})
	dir := t.TempDir()
	pack, key := writePack(t, dir)
	stamped := filepath.Join(dir, "stamped.json")

	out, err := runEvidence(t, "timestamp", pack, "--tsa", tsa.URL, "--tsa-ca", tsa.RootFile, "-o", stamped)
	if err != nil || !strings.Contains(out, "Timestamped") {
		t.Fatalf("timestamp: %v\n%s", err, out)
	}
	if _, err := runEvidence(t, "timestamp", stamped, "--tsa", tsa.URL, "--tsa-ca", tsa.RootFile); err == nil {
		t.Error("stamping twice succeeded")
	}

	out, err = runEvidence(t, "verify", stamped, "--key", key, "--tsa-ca", tsa.RootFile, "--strict")
	if err != nil || !strings.Contains(out, "Timestamp ") || !strings.Contains(out, "VaultRun Test TSA") || strings.Contains(out, "no external timestamp") {
		t.Fatalf("verify stamped pack: %v\n%s", err, out)
	}

	// Without the TSA's root the timestamp is a warning, which --strict fails.
	out, err = runEvidence(t, "verify", stamped, "--key", key)
	if err != nil || !strings.Contains(out, "not trusted here") {
		t.Errorf("untrusted TSA: %v\n%s", err, out)
	}
	if _, err := runEvidence(t, "verify", stamped, "--key", key, "--strict"); err == nil {
		t.Error("--strict passed with an untrusted timestamp")
	}

	// A timestamp moved onto another pack does not fit it.
	other, otherKey := writePack(t, t.TempDir())
	var a, b map[string]any
	sb, _ := os.ReadFile(stamped)
	ob, _ := os.ReadFile(other)
	_ = json.Unmarshal(sb, &a)
	_ = json.Unmarshal(ob, &b)
	b["timestamp"] = a["timestamp"]
	moved, _ := json.Marshal(b)
	_ = os.WriteFile(other, moved, 0o600)
	if _, err := runEvidence(t, "verify", other, "--key", otherKey, "--tsa-ca", tsa.RootFile); err == nil || !strings.Contains(err.Error(), "NOT VALID") {
		t.Errorf("moved timestamp: want NOT VALID, got %v", err)
	}
}

func TestEvidenceExportAll(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"sessions":[
			{"id":"s-old","created_at":"2026-09-30T23:59:00Z"},
			{"id":"s-in","created_at":"2026-10-05T10:00:00Z"},
			{"id":"s-broken","created_at":"2026-10-06T10:00:00Z"},
			{"id":"s-late","created_at":"2026-11-01T00:00:00Z"}],
			"pagination":{"total":4}}`)
	})
	mux.HandleFunc("/api/v1/sessions/s-in/evidence", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"pack":"e30="}`)
	})
	mux.HandleFunc("/api/v1/sessions/s-broken/evidence", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	prevURL, prevKey := apiURL, apiKey
	apiURL, apiKey = srv.URL, "test"
	defer func() { apiURL, apiKey = prevURL, prevKey }()

	dir := filepath.Join(t.TempDir(), "packs")
	out, err := runEvidence(t, "export", "--all", "--since", "2026-10-01", "--until", "2026-11-01", "-d", dir)
	if err == nil || !strings.Contains(err.Error(), "1 sessions could not be exported") {
		t.Fatalf("want an error for the broken session, got %v\n%s", err, out)
	}
	if !strings.Contains(out, "Exported 1 of 2 sessions") {
		t.Errorf("output:\n%s", out)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "s-in.json" {
		t.Errorf("exported files = %v", entries)
	}
	if _, err := runEvidence(t, "export", "--all", "s-in"); err == nil {
		t.Error("--all with a session id accepted")
	}
	if _, err := runEvidence(t, "export"); err == nil {
		t.Error("export without id or --all accepted")
	}
}
