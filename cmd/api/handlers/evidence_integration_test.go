//go:build integration

package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/nickvd7/vaultrun/cmd/api/handlers"
	"github.com/nickvd7/vaultrun/cmd/api/middleware"
	"github.com/nickvd7/vaultrun/internal/audit"
	"github.com/nickvd7/vaultrun/internal/config"
	"github.com/nickvd7/vaultrun/internal/evidence"
	"github.com/nickvd7/vaultrun/internal/evidence/evidencetest"
	"github.com/nickvd7/vaultrun/internal/models"
	"github.com/nickvd7/vaultrun/internal/policy"
	"github.com/nickvd7/vaultrun/internal/workspace"
)

// newEvidenceRouter serves the evidence and key endpoints; signer may be nil.
func newEvidenceRouter(t *testing.T, db *sqlx.DB, signer *evidence.Signer, tsa ...*evidencetest.TSA) (*gin.Engine, *audit.Logger) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	cfg := &config.Config{
		Auth:      config.AuthConfig{MasterKey: testMasterKey},
		Workspace: config.WorkspaceConfig{BaseDir: t.TempDir(), MaxFileMB: 100},
	}
	al := audit.New(db, "")
	hub := handlers.NewHub(db, nil, workspace.New(cfg.Workspace.BaseDir), nil, al, cfg, policy.AllowAll{}, nil, nil, nil, nil)
	if signer != nil {
		hub.SetEvidenceSigner(signer)
	}
	if len(tsa) > 0 {
		hub.SetEvidenceTSA(tsa[0].URL, tsa[0].Roots)
	}
	api := r.Group("/api/v1", middleware.APIKeyAuth(db, testMasterKey, nil))
	keysH := handlers.NewKeyHandler(hub)
	api.POST("/keys", keysH.Create)
	evH := handlers.NewEvidenceHandler(hub)
	api.GET("/sessions/:id/evidence", evH.Export)
	api.GET("/evidence/public-key", evH.PublicKey)
	return r, al
}

func newSignerT(t *testing.T) *evidence.Signer {
	t.Helper()
	seed, _, err := evidence.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	s, err := evidence.ParseSigningKey(seed)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// createUserKey returns a new key's plaintext and its actor id.
func createUserKey(t *testing.T, r *gin.Engine, name string) (key, actor string) {
	t.Helper()
	w := rec(r, "POST", "/api/v1/keys", `{"name":"`+name+`"}`, masterHdr())
	if w.Code != http.StatusCreated {
		t.Fatalf("create key: %d %s", w.Code, w.Body)
	}
	var created map[string]any
	_ = json.NewDecoder(w.Body).Decode(&created)
	return created["key"].(string), created["id"].(string)
}

func TestEvidenceNotConfigured(t *testing.T) {
	truncateAll(t)
	r, _ := newEvidenceRouter(t, testDB, nil)
	id := uuid.NewString()
	insertFakeSession(t, id, "master")
	for _, path := range []string{"/api/v1/sessions/" + id + "/evidence", "/api/v1/evidence/public-key"} {
		if w := rec(r, "GET", path, "", masterHdr()); w.Code != http.StatusNotImplemented {
			t.Errorf("%s: want 501, got %d: %s", path, w.Code, w.Body)
		}
	}
}

func TestEvidenceExportScopeAndAccess(t *testing.T) {
	truncateAll(t)
	signer := newSignerT(t)
	r, al := newEvidenceRouter(t, testDB, signer)
	ownerKey, owner := createUserKey(t, r, "owner")
	otherKey, other := createUserKey(t, r, "other")

	id := uuid.New()
	insertFakeSession(t, id.String(), owner)
	ctx := context.Background()
	al.Log(ctx, audit.Event{Actor: owner, SessionID: &id, Action: models.ActionSessionCreated,
		Metadata: models.JSONB{"image": "python:3.12-slim", "runtime": "runsc"}})
	al.Log(ctx, audit.Event{Actor: owner, SessionID: &id, Action: models.ActionCommandStarted})
	al.Log(ctx, audit.Event{Actor: "master", SessionID: &id, Action: models.ActionFileDownloaded})

	export := func(key string) (int, *evidence.Pack) {
		w := rec(r, "GET", "/api/v1/sessions/"+id.String()+"/evidence", "", map[string]string{"X-API-Key": key})
		if w.Code != http.StatusOK {
			return w.Code, nil
		}
		p, err := evidence.Verify(w.Body.Bytes(), signer.PublicKey())
		if err != nil {
			t.Fatalf("exported pack does not verify: %v", err)
		}
		return w.Code, p
	}

	// The owner sees only its own entries, like GET /audit.
	code, p := export(ownerKey)
	if code != http.StatusOK {
		t.Fatalf("owner export: %d", code)
	}
	if p.Audit.Scope != evidence.AuditScopeActor || len(p.Audit.Entries) != 2 || p.ExportedBy != owner {
		t.Errorf("owner pack: scope=%s entries=%d exported_by=%s", p.Audit.Scope, len(p.Audit.Entries), p.ExportedBy)
	}
	if p.Session.Runtime != "runsc" || p.Session.CreatedByName != "owner" {
		t.Errorf("owner pack session: %+v", p.Session)
	}
	if p.Audit.Entries[0].Action != models.ActionSessionCreated {
		t.Errorf("entries not oldest first: %+v", p.Audit.Entries)
	}

	// The master key gets every entry, its own included.
	if _, p := export(testMasterKey); p == nil || p.Audit.Scope != evidence.AuditScopeAll || len(p.Audit.Entries) != 4 {
		// 3 logged above + the owner's evidence.exported entry
		t.Errorf("master pack: %+v", p)
	}

	// Another key cannot export (or learn that) the session.
	if code, _ := export(otherKey); code != http.StatusNotFound {
		t.Errorf("other key: want 404, got %d", code)
	}
	_ = other

	if w := rec(r, "GET", "/api/v1/sessions/not-a-uuid/evidence", "", masterHdr()); w.Code != http.StatusBadRequest {
		t.Errorf("bad uuid: want 400, got %d", w.Code)
	}
	if w := rec(r, "GET", "/api/v1/sessions/"+uuid.NewString()+"/evidence", "", masterHdr()); w.Code != http.StatusNotFound {
		t.Errorf("unknown session: want 404, got %d", w.Code)
	}

	// The public key endpoint serves the key packs verify with.
	w := rec(r, "GET", "/api/v1/evidence/public-key", "", map[string]string{"X-API-Key": ownerKey})
	var pk struct {
		KeyID string `json:"key_id"`
		PEM   string `json:"public_key_pem"`
	}
	_ = json.NewDecoder(w.Body).Decode(&pk)
	pub, err := evidence.ParsePublicKeyPEM([]byte(pk.PEM))
	if err != nil || !pub.Equal(signer.PublicKey()) || pk.KeyID != signer.KeyID() {
		t.Errorf("public key endpoint: %v %+v", err, pk)
	}
}

func TestEvidenceExportTimestamped(t *testing.T) {
	truncateAll(t)
	tsa := evidencetest.NewTSA(t, evidencetest.Options{})
	signer := newSignerT(t)
	r, _ := newEvidenceRouter(t, testDB, signer, tsa)
	id := uuid.NewString()
	insertFakeSession(t, id, "master")

	w := rec(r, "GET", "/api/v1/sessions/"+id+"/evidence", "", masterHdr())
	if w.Code != http.StatusOK || w.Header().Get("X-Evidence-Timestamp") == "" {
		t.Fatalf("export: %d %s (timestamp header %q)", w.Code, w.Body, w.Header().Get("X-Evidence-Timestamp"))
	}
	env, err := evidence.Open(w.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.Verify(signer.PublicKey()); err != nil {
		t.Fatalf("pack signature: %v", err)
	}
	if _, err := evidence.VerifyTimestamp(env, tsa.Roots); err != nil {
		t.Fatalf("pack timestamp: %v", err)
	}

	// Asked for timestamps, the server hands out none without one.
	tsa.Fail = true
	if w := rec(r, "GET", "/api/v1/sessions/"+id+"/evidence", "", masterHdr()); w.Code != http.StatusBadGateway {
		t.Errorf("TSA down: want 502, got %d: %s", w.Code, w.Body)
	}
	// A TSA that does not chain to the configured roots is refused too.
	untrusted := evidencetest.NewTSA(t, evidencetest.Options{})
	r2, _ := newEvidenceRouter(t, testDB, signer, &evidencetest.TSA{URL: untrusted.URL, Roots: tsa.Roots})
	if w := rec(r2, "GET", "/api/v1/sessions/"+id+"/evidence", "", masterHdr()); w.Code != http.StatusBadGateway {
		t.Errorf("untrusted TSA: want 502, got %d: %s", w.Code, w.Body)
	}
}
