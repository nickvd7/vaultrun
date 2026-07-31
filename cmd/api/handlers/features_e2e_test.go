//go:build integration

package handlers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/nickvd7/vaultrun/cmd/api/handlers"
	"github.com/nickvd7/vaultrun/cmd/api/middleware"
	"github.com/nickvd7/vaultrun/internal/audit"
	"github.com/nickvd7/vaultrun/internal/config"
	"github.com/nickvd7/vaultrun/internal/cost"
	"github.com/nickvd7/vaultrun/internal/policy"
	"github.com/nickvd7/vaultrun/internal/replay"
	"github.com/nickvd7/vaultrun/internal/templates"
	"github.com/nickvd7/vaultrun/internal/workspace"
)

// testHMACKey is used for both checkpoint and cost-metric signing so tests can
// assert that tampering is detected.
const testHMACKey = "e2e-test-hmac-key-not-for-production"

// stubWorkspace stands in for the real workspace manager. Snapshot creation
// touches the filesystem and Docker in production; here it records the calls so
// tests can assert the manager was driven correctly without either dependency.
type stubWorkspace struct {
	created    []uuid.UUID
	restored   []string
	deleted    []string
	workspaces []uuid.UUID
	sizeB      int64
	failNext   error
}

func (s *stubWorkspace) Create(sessionID uuid.UUID) (string, error) {
	s.workspaces = append(s.workspaces, sessionID)
	return fmt.Sprintf("/tmp/vaultrun-test/ws-%s", sessionID), nil
}

func (s *stubWorkspace) Delete(sessionID uuid.UUID) error {
	return nil
}

func (s *stubWorkspace) CreateSnapshot(sessionID, snapshotID uuid.UUID) (string, int64, error) {
	if s.failNext != nil {
		err := s.failNext
		s.failNext = nil
		return "", 0, err
	}
	s.created = append(s.created, snapshotID)
	size := s.sizeB
	if size == 0 {
		size = 1024
	}
	return fmt.Sprintf("/tmp/vaultrun-test/%s.tar.gz", snapshotID), size, nil
}

func (s *stubWorkspace) RestoreSnapshot(sessionID uuid.UUID, archivePath string) error {
	s.restored = append(s.restored, archivePath)
	return nil
}

func (s *stubWorkspace) DeleteSnapshot(archivePath string) error {
	s.deleted = append(s.deleted, archivePath)
	return nil
}

// featureRouter builds a router with the replay, template and cost endpoints
// registered. It mirrors newRouter's wiring for those features.
//
// The optional tweaks adjust the config before the handlers are built, which
// lets a test exercise deployment limits (image allowlist, resource ceilings,
// session quota) without a second router builder.
func featureRouter(t *testing.T, db *sqlx.DB, tweaks ...func(*config.Config)) (*gin.Engine, *stubWorkspace, *templates.Manager) {
	t.Helper()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())

	cfg := &config.Config{
		Auth:      config.AuthConfig{MasterKey: testMasterKey},
		Docker:    config.DockerConfig{DefaultImage: "python:3.12-slim"},
		Workspace: config.WorkspaceConfig{BaseDir: os.TempDir(), MaxFileMB: 100},
	}
	for _, tweak := range tweaks {
		tweak(cfg)
	}

	al := audit.New(db, testHMACKey)
	ws := workspace.New(cfg.Workspace.BaseDir)
	hub := handlers.NewHub(db, nil, ws, nil, al, cfg, policy.AllowAll{}, nil, nil, nil, nil)
	authMW := middleware.APIKeyAuth(db, testMasterKey, nil)

	api := r.Group("/api/v1", authMW)

	// Key issuance is needed to obtain non-master principals for the
	// cross-tenant tests.
	keysH := handlers.NewKeyHandler(hub)
	api.POST("/keys", keysH.Create)

	stubWS := &stubWorkspace{}
	replayMgr := replay.New(db, stubWS, []byte(testHMACKey))
	replayH := handlers.NewReplayHandler(hub, replayMgr)
	api.POST("/sessions/:id/checkpoints", replayH.CreateCheckpoint)
	api.GET("/sessions/:id/checkpoints", replayH.ListCheckpoints)
	api.GET("/sessions/:id/checkpoints/:checkpoint_id", replayH.GetCheckpoint)
	api.POST("/sessions/:id/checkpoints/:checkpoint_id/restore", replayH.RestoreCheckpoint)
	api.POST("/checkpoints/:checkpoint_id/fork", replayH.ForkCheckpoint)
	api.DELETE("/sessions/:id/checkpoints/:checkpoint_id", replayH.DeleteCheckpoint)

	tmplMgr := templates.New(db)
	tmplH := handlers.NewTemplateHandler(tmplMgr, hub)
	api.GET("/templates", tmplH.ListTemplates)
	api.GET("/templates/:id", tmplH.GetTemplate)
	api.GET("/templates/slug/:slug", tmplH.GetTemplateBySlug)
	api.POST("/templates", tmplH.CreateTemplate)
	api.PATCH("/templates/:id", tmplH.UpdateTemplate)
	api.DELETE("/templates/:id", tmplH.DeleteTemplate)
	api.POST("/templates/:id/use", tmplH.CreateSessionFromTemplate)

	costTracker := cost.New(db, []byte(testHMACKey))
	costH := handlers.NewCostHandler(hub, costTracker)
	api.GET("/costs/sessions/:id", costH.GetSessionCosts)
	api.GET("/costs/breakdown", costH.GetCostBreakdown)
	api.GET("/costs/alerts", costH.GetAlerts)
	api.POST("/costs/alerts/:id/resolve", costH.ResolveAlert)
	api.GET("/orgs/:id/costs", costH.GetOrgCosts)
	api.POST("/orgs/:id/budget", costH.SetBudget)

	nlH := handlers.NewNLPolicyHandler(hub)
	api.POST("/policies/parse", nlH.ParsePolicy)
	api.POST("/policies/validate", nlH.ValidatePolicy)
	api.POST("/policies/compile", nlH.CompilePolicy)
	api.GET("/policies/templates", nlH.ListTemplates)
	api.GET("/policies/templates/:name", nlH.GetTemplate)
	api.POST("/policies/from-template/:name", nlH.FromTemplate)

	return r, stubWS, tmplMgr
}

// truncateFeatureTables clears the tables owned by the new features in addition
// to the core ones, so each test starts from a known state.
func truncateFeatureTables(t *testing.T) {
	t.Helper()

	_, err := testDB.Exec(`
		TRUNCATE
			replay_checkpoints, session_snapshots,
			cost_metrics, cost_budgets, cost_alerts,
			template_usage, session_templates,
			session_agents, agent_messages, file_versions,
			audit_logs, files, runs, sessions, api_keys, org_members, organizations
		CASCADE
	`)
	if err != nil {
		t.Fatalf("truncate feature tables: %v", err)
	}
}

// seedSession inserts a session owned by the given principal and returns its ID.
func seedFeatureSession(t *testing.T, createdBy string, orgID *uuid.UUID, replayEnabled bool) uuid.UUID {
	t.Helper()

	id := uuid.New()
	_, err := testDB.Exec(`
		INSERT INTO sessions (id, image, status, workspace_path, created_by, org_id, replay_enabled)
		VALUES ($1, 'python:3.12-slim', 'running', $2, $3, $4, $5)
	`, id, "/tmp/vaultrun-test/"+id.String(), createdBy, orgID, replayEnabled)
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return id
}

// seedOrgWithMember creates an organization with one member and returns its ID.
func seedOrgWithMember(t *testing.T, name, principal, role string) uuid.UUID {
	t.Helper()

	orgID := uuid.New()
	_, err := testDB.Exec(
		`INSERT INTO organizations (id, name, slug) VALUES ($1, $2, $3)`,
		orgID, name, name)
	if err != nil {
		t.Fatalf("seed org: %v", err)
	}
	_, err = testDB.Exec(
		`INSERT INTO org_members (org_id, principal, role) VALUES ($1, $2, $3)`,
		orgID, principal, role)
	if err != nil {
		t.Fatalf("seed org member: %v", err)
	}
	return orgID
}

// issueKey creates an API key and returns the raw key plus the principal the
// auth middleware will report for it.
//
// The principal is the key's UUID, not its name (see middleware.APIKeyAuth), so
// anything that grants access — org membership, session ownership — has to be
// keyed on the UUID.
func issueKey(t *testing.T, r *gin.Engine, name string) (key, principal string) {
	t.Helper()

	w := rec(r, "POST", "/api/v1/keys", fmt.Sprintf(`{"name":%q}`, name), masterHdr())
	if w.Code != http.StatusCreated {
		t.Fatalf("create key %q: want 201, got %d: %s", name, w.Code, w.Body)
	}

	var resp struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode key response: %v", err)
	}
	if resp.ID == "" || resp.Key == "" {
		t.Fatalf("key response missing id or key: %s", w.Body)
	}
	return resp.Key, resp.ID
}

func keyHdr(key string) map[string]string { return map[string]string{"X-API-Key": key} }

// ── Session Replay ───────────────────────────────────────────────────────────

// TestReplayCheckpointLifecycle walks the full flow an agent would perform:
// create a checkpoint, list it, read it back, restore it, fork it, delete it.
func TestReplayCheckpointLifecycle(t *testing.T) {
	truncateFeatureTables(t)
	r, stubWS, _ := featureRouter(t, testDB)

	sessionID := seedFeatureSession(t, "master", nil, true)

	// Create
	w := rec(r, "POST", "/api/v1/sessions/"+sessionID.String()+"/checkpoints",
		`{"name":"before refactor","description":"tests green"}`, masterHdr())
	if w.Code != http.StatusCreated {
		t.Fatalf("create checkpoint: want 201, got %d: %s", w.Code, w.Body)
	}

	var created struct {
		ID               uuid.UUID `json:"id"`
		CheckpointNumber int       `json:"checkpoint_number"`
		Signature        string    `json:"signature"`
		ArchivePath      string    `json:"archive_path"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created checkpoint: %v", err)
	}

	if created.CheckpointNumber != 1 {
		t.Errorf("first checkpoint number = %d, want 1", created.CheckpointNumber)
	}
	if created.Signature == "" {
		t.Error("created checkpoint has an empty signature — tamper detection is inert")
	}
	// The archive path is a host filesystem path and must never be serialised.
	if created.ArchivePath != "" {
		t.Errorf("checkpoint response exposes archive_path = %q, want it omitted", created.ArchivePath)
	}
	if len(stubWS.created) != 1 {
		t.Errorf("workspace snapshots created = %d, want 1", len(stubWS.created))
	}

	// List
	w = rec(r, "GET", "/api/v1/sessions/"+sessionID.String()+"/checkpoints", "", masterHdr())
	if w.Code != http.StatusOK {
		t.Fatalf("list checkpoints: want 200, got %d: %s", w.Code, w.Body)
	}
	var list struct {
		Checkpoints []map[string]any `json:"checkpoints"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode checkpoint list: %v", err)
	}
	if len(list.Checkpoints) != 1 {
		t.Fatalf("listed %d checkpoints, want 1", len(list.Checkpoints))
	}

	// Get
	w = rec(r, "GET",
		"/api/v1/sessions/"+sessionID.String()+"/checkpoints/"+created.ID.String(), "", masterHdr())
	if w.Code != http.StatusOK {
		t.Fatalf("get checkpoint: want 200, got %d: %s", w.Code, w.Body)
	}

	// Restore
	w = rec(r, "POST",
		"/api/v1/sessions/"+sessionID.String()+"/checkpoints/"+created.ID.String()+"/restore",
		"", masterHdr())
	if w.Code != http.StatusOK {
		t.Fatalf("restore checkpoint: want 200, got %d: %s", w.Code, w.Body)
	}
	if len(stubWS.restored) != 1 {
		t.Errorf("workspace restores = %d, want 1", len(stubWS.restored))
	}

	// Fork
	w = rec(r, "POST", "/api/v1/checkpoints/"+created.ID.String()+"/fork",
		`{"name":"experiment"}`, masterHdr())
	if w.Code != http.StatusCreated {
		t.Fatalf("fork checkpoint: want 201, got %d: %s", w.Code, w.Body)
	}
	var forked struct {
		ID                     uuid.UUID  `json:"id"`
		ForkedFromCheckpointID *uuid.UUID `json:"forked_from_checkpoint_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &forked); err != nil {
		t.Fatalf("decode forked session: %v", err)
	}
	if forked.ID == sessionID {
		t.Error("fork returned the original session instead of a new one")
	}

	// Delete
	w = rec(r, "DELETE",
		"/api/v1/sessions/"+sessionID.String()+"/checkpoints/"+created.ID.String(), "", masterHdr())
	if w.Code != http.StatusOK && w.Code != http.StatusNoContent {
		t.Fatalf("delete checkpoint: want 200/204, got %d: %s", w.Code, w.Body)
	}

	// Gone
	w = rec(r, "GET",
		"/api/v1/sessions/"+sessionID.String()+"/checkpoints/"+created.ID.String(), "", masterHdr())
	if w.Code != http.StatusNotFound {
		t.Errorf("get deleted checkpoint: want 404, got %d: %s", w.Code, w.Body)
	}
}

// TestReplayCheckpointNumbersAreSequential verifies numbering is per session and
// gapless, which the UI relies on to order the timeline.
func TestReplayCheckpointNumbersAreSequential(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	sessionID := seedFeatureSession(t, "master", nil, true)

	for i := 1; i <= 5; i++ {
		w := rec(r, "POST", "/api/v1/sessions/"+sessionID.String()+"/checkpoints",
			fmt.Sprintf(`{"description":"step %d"}`, i), masterHdr())
		if w.Code != http.StatusCreated {
			t.Fatalf("create checkpoint %d: want 201, got %d: %s", i, w.Code, w.Body)
		}

		var cp struct {
			CheckpointNumber int `json:"checkpoint_number"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &cp); err != nil {
			t.Fatalf("decode checkpoint %d: %v", i, err)
		}
		if cp.CheckpointNumber != i {
			t.Errorf("checkpoint %d has number %d, want %d", i, cp.CheckpointNumber, i)
		}
	}
}

// TestReplayCrossSessionIsolation: a checkpoint belonging to one session must
// not be reachable through another session's path, even for the same caller.
// Otherwise the session ID in the URL becomes decorative and any authenticated
// caller could enumerate checkpoints.
func TestReplayCrossSessionIsolation(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	sessionA := seedFeatureSession(t, "master", nil, true)
	sessionB := seedFeatureSession(t, "master", nil, true)

	w := rec(r, "POST", "/api/v1/sessions/"+sessionA.String()+"/checkpoints",
		`{"description":"in session A"}`, masterHdr())
	if w.Code != http.StatusCreated {
		t.Fatalf("create checkpoint: want 201, got %d: %s", w.Code, w.Body)
	}
	var cp struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &cp); err != nil {
		t.Fatalf("decode checkpoint: %v", err)
	}

	// Reading A's checkpoint through B's path must fail.
	w = rec(r, "GET",
		"/api/v1/sessions/"+sessionB.String()+"/checkpoints/"+cp.ID.String(), "", masterHdr())
	if w.Code == http.StatusOK {
		t.Errorf("checkpoint from session A is readable through session B: got 200, body %s", w.Body)
	}

	// So must restoring it.
	w = rec(r, "POST",
		"/api/v1/sessions/"+sessionB.String()+"/checkpoints/"+cp.ID.String()+"/restore", "", masterHdr())
	if w.Code == http.StatusOK {
		t.Errorf("checkpoint from session A is restorable into session B: got 200, body %s", w.Body)
	}

	// And deleting it.
	w = rec(r, "DELETE",
		"/api/v1/sessions/"+sessionB.String()+"/checkpoints/"+cp.ID.String(), "", masterHdr())
	if w.Code == http.StatusOK || w.Code == http.StatusNoContent {
		t.Errorf("checkpoint from session A is deletable through session B: got %d", w.Code)
	}

	// B's own list must stay empty.
	w = rec(r, "GET", "/api/v1/sessions/"+sessionB.String()+"/checkpoints", "", masterHdr())
	var list struct {
		Checkpoints []map[string]any `json:"checkpoints"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Checkpoints) != 0 {
		t.Errorf("session B lists %d checkpoints, want 0", len(list.Checkpoints))
	}
}

// TestCheckpointRowsAreImmutable verifies the first line of defence: the
// database trigger added in migration 015 rejects any UPDATE on
// replay_checkpoints. The application never updates a checkpoint, so a trigger
// costs nothing and turns tampering from detectable into impossible for anyone
// holding ordinary table privileges.
func TestCheckpointRowsAreImmutable(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	sessionID := seedFeatureSession(t, "master", nil, true)

	w := rec(r, "POST", "/api/v1/sessions/"+sessionID.String()+"/checkpoints",
		`{"description":"authentic"}`, masterHdr())
	if w.Code != http.StatusCreated {
		t.Fatalf("create checkpoint: want 201, got %d: %s", w.Code, w.Body)
	}
	var cp struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &cp); err != nil {
		t.Fatalf("decode checkpoint: %v", err)
	}

	updates := []struct {
		name  string
		query string
	}{
		{"command", `UPDATE replay_checkpoints SET command = 'rm -rf /' WHERE id = $1`},
		{"exit code", `UPDATE replay_checkpoints SET exit_code = 0 WHERE id = $1`},
		{"stdout", `UPDATE replay_checkpoints SET stdout_preview = 'all tests passed' WHERE id = $1`},
		{"signature", `UPDATE replay_checkpoints SET signature = repeat('0', 64) WHERE id = $1`},
		{"archive path", `UPDATE replay_checkpoints SET archive_path = '/etc/passwd' WHERE id = $1`},
	}

	for _, tc := range updates {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := testDB.Exec(tc.query, cp.ID); err == nil {
				t.Errorf("UPDATE of %s succeeded, want it blocked by the immutability trigger", tc.name)
			}
		})
	}
}

// TestReplayTamperDetection verifies the second line of defence: even with the
// immutability trigger disabled — as a database superuser, or after a restore
// from a modified dump — a rewritten checkpoint fails its HMAC check and cannot
// be restored.
func TestReplayTamperDetection(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	tampers := []struct {
		name  string
		query string
	}{
		{"rewrite command", `UPDATE replay_checkpoints SET command = 'rm -rf /' WHERE id = $1`},
		{"rewrite exit code", `UPDATE replay_checkpoints SET exit_code = 99 WHERE id = $1`},
		{"rewrite stdout", `UPDATE replay_checkpoints SET stdout_preview = 'all tests passed' WHERE id = $1`},
		{"rewrite duration", `UPDATE replay_checkpoints SET duration_ms = 1 WHERE id = $1`},
		{"rewrite size", `UPDATE replay_checkpoints SET size_bytes = 1 WHERE id = $1`},
		{"forge signature", `UPDATE replay_checkpoints SET signature = repeat('0', 64) WHERE id = $1`},
		{"clear signature", `UPDATE replay_checkpoints SET signature = '' WHERE id = $1`},
	}

	for _, tc := range tampers {
		t.Run(tc.name, func(t *testing.T) {
			truncateFeatureTables(t)
			sid := seedFeatureSession(t, "master", nil, true)

			w := rec(r, "POST", "/api/v1/sessions/"+sid.String()+"/checkpoints",
				`{"description":"authentic"}`, masterHdr())
			if w.Code != http.StatusCreated {
				t.Fatalf("create checkpoint: want 201, got %d: %s", w.Code, w.Body)
			}
			var fresh struct {
				ID uuid.UUID `json:"id"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &fresh); err != nil {
				t.Fatalf("decode checkpoint: %v", err)
			}

			// Bypass the trigger to isolate the HMAC check.
			if _, err := testDB.Exec(
				`ALTER TABLE replay_checkpoints DISABLE TRIGGER replay_checkpoints_immutable`); err != nil {
				t.Fatalf("disable immutability trigger: %v", err)
			}
			defer func() {
				if _, err := testDB.Exec(
					`ALTER TABLE replay_checkpoints ENABLE TRIGGER replay_checkpoints_immutable`); err != nil {
					t.Fatalf("re-enable immutability trigger: %v", err)
				}
			}()

			if _, err := testDB.Exec(tc.query, fresh.ID); err != nil {
				t.Fatalf("tamper query failed: %v", err)
			}

			w = rec(r, "POST",
				"/api/v1/sessions/"+sid.String()+"/checkpoints/"+fresh.ID.String()+"/restore",
				"", masterHdr())
			if w.Code == http.StatusOK {
				t.Errorf("restore succeeded after %s — HMAC verification did not catch it", tc.name)
			}
		})
	}
}

// TestReplayRejectsDisabledSession: creating a checkpoint on a session that has
// not opted into replay must fail, otherwise the opt-in is meaningless.
func TestReplayRejectsDisabledSession(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	sessionID := seedFeatureSession(t, "master", nil, false)

	w := rec(r, "POST", "/api/v1/sessions/"+sessionID.String()+"/checkpoints",
		`{"description":"should be refused"}`, masterHdr())
	if w.Code == http.StatusCreated {
		t.Errorf("checkpoint created for a session with replay_enabled = false: got %d", w.Code)
	}
}

func TestReplayRejectsUnknownSession(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	w := rec(r, "POST", "/api/v1/sessions/"+uuid.New().String()+"/checkpoints",
		`{"description":"no such session"}`, masterHdr())
	if w.Code != http.StatusNotFound && w.Code != http.StatusForbidden {
		t.Errorf("checkpoint on unknown session: want 404/403, got %d: %s", w.Code, w.Body)
	}
}

func TestReplayRejectsMalformedIDs(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	sessionID := seedFeatureSession(t, "master", nil, true)

	malformed := []struct {
		name string
		path string
	}{
		{"not a uuid", "/api/v1/sessions/not-a-uuid/checkpoints"},
		// URL-encoded: httptest.NewRequest rejects a raw space in the target.
		{"sql fragment", "/api/v1/sessions/1'%20OR%20'1'='1/checkpoints"},
		{"sql comment", "/api/v1/sessions/1--/checkpoints"},
		{"path traversal", "/api/v1/sessions/..%2f..%2fetc%2fpasswd/checkpoints"},
		{"null byte", "/api/v1/sessions/" + sessionID.String() + "%00/checkpoints"},
		{"empty checkpoint id", "/api/v1/sessions/" + sessionID.String() + "/checkpoints/"},
		{"malformed checkpoint id", "/api/v1/sessions/" + sessionID.String() + "/checkpoints/xyz"},
	}

	for _, tc := range malformed {
		t.Run(tc.name, func(t *testing.T) {
			w := rec(r, "GET", tc.path, "", masterHdr())
			if w.Code == http.StatusOK {
				t.Errorf("GET %s returned 200, want a client error", tc.path)
			}
			if w.Code >= 500 {
				t.Errorf("GET %s returned %d — a malformed ID should be a 4xx, not a server error: %s",
					tc.path, w.Code, w.Body)
			}
		})
	}
}

// TestReplayRequiresAuthentication guards every checkpoint route against
// unauthenticated access.
func TestReplayRequiresAuthentication(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	sessionID := seedFeatureSession(t, "master", nil, true)
	sid := sessionID.String()
	cid := uuid.New().String()

	routes := []struct {
		method string
		path   string
	}{
		{"POST", "/api/v1/sessions/" + sid + "/checkpoints"},
		{"GET", "/api/v1/sessions/" + sid + "/checkpoints"},
		{"GET", "/api/v1/sessions/" + sid + "/checkpoints/" + cid},
		{"POST", "/api/v1/sessions/" + sid + "/checkpoints/" + cid + "/restore"},
		{"POST", "/api/v1/checkpoints/" + cid + "/fork"},
		{"DELETE", "/api/v1/sessions/" + sid + "/checkpoints/" + cid},
	}

	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			// No key at all.
			w := rec(r, rt.method, rt.path, "{}", nil)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("%s %s without a key: want 401, got %d", rt.method, rt.path, w.Code)
			}

			// A syntactically valid but unknown key.
			w = rec(r, rt.method, rt.path, "{}", keyHdr("vr_deadbeefdeadbeefdeadbeefdeadbeef"))
			if w.Code != http.StatusUnauthorized {
				t.Errorf("%s %s with an invalid key: want 401, got %d", rt.method, rt.path, w.Code)
			}
		})
	}
}

// TestReplayCrossTenantIsolation: a caller who is not a member of the owning
// org must not reach another tenant's checkpoints.
func TestReplayCrossTenantIsolation(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	// Two orgs, one member each.
	aliceKey, alice := issueKey(t, r, "alice")
	bobKey, bob := issueKey(t, r, "bob")

	orgA := seedOrgWithMember(t, "org-a", alice, "admin")
	seedOrgWithMember(t, "org-b", bob, "admin")

	sessionA := seedFeatureSession(t, alice, &orgA, true)

	// Alice can create a checkpoint in her own session.
	w := rec(r, "POST", "/api/v1/sessions/"+sessionA.String()+"/checkpoints",
		`{"description":"alice's work"}`, keyHdr(aliceKey))
	if w.Code != http.StatusCreated {
		t.Fatalf("alice create checkpoint: want 201, got %d: %s", w.Code, w.Body)
	}
	var cp struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &cp); err != nil {
		t.Fatalf("decode checkpoint: %v", err)
	}

	// Bob must not be able to read, restore, fork or delete it.
	attempts := []struct {
		name   string
		method string
		path   string
	}{
		{"list", "GET", "/api/v1/sessions/" + sessionA.String() + "/checkpoints"},
		{"get", "GET", "/api/v1/sessions/" + sessionA.String() + "/checkpoints/" + cp.ID.String()},
		{"restore", "POST", "/api/v1/sessions/" + sessionA.String() + "/checkpoints/" + cp.ID.String() + "/restore"},
		{"fork", "POST", "/api/v1/checkpoints/" + cp.ID.String() + "/fork"},
		{"delete", "DELETE", "/api/v1/sessions/" + sessionA.String() + "/checkpoints/" + cp.ID.String()},
	}

	for _, a := range attempts {
		t.Run("bob cannot "+a.name, func(t *testing.T) {
			w := rec(r, a.method, a.path, "{}", keyHdr(bobKey))
			if w.Code == http.StatusOK || w.Code == http.StatusCreated || w.Code == http.StatusNoContent {
				t.Errorf("bob %s succeeded on alice's checkpoint: got %d, body %s", a.name, w.Code, w.Body)
			}
		})
	}
}

// ── Session Templates ────────────────────────────────────────────────────────

// TestTemplateLifecycle walks create, read by id and slug, update, list and
// delete.
func TestTemplateLifecycle(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	// Templates are org-scoped, so the caller needs an org.
	key, principal := issueKey(t, r, "carol")
	seedOrgWithMember(t, "carol-org", principal, "admin")

	body := `{
		"slug": "my-python-env",
		"name": "My Python Env",
		"description": "Python 3.12 with pytest",
		"category": "data-science",
		"tags": ["python", "testing"],
		"image": "python:3.12-slim",
		"version": "1.0.0",
		"resources": {"cpu_limit": 2, "memory_limit_mb": 4096, "timeout_seconds": 1800},
		"network": {"enabled": true, "allowed_hosts": ["pypi.org"]}
	}`

	w := rec(r, "POST", "/api/v1/templates", body, keyHdr(key))
	if w.Code != http.StatusCreated {
		t.Fatalf("create template: want 201, got %d: %s", w.Code, w.Body)
	}
	var created struct {
		ID   uuid.UUID `json:"id"`
		Slug string    `json:"slug"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created template: %v", err)
	}

	// Get by id
	w = rec(r, "GET", "/api/v1/templates/"+created.ID.String(), "", keyHdr(key))
	if w.Code != http.StatusOK {
		t.Fatalf("get template by id: want 200, got %d: %s", w.Code, w.Body)
	}

	// Get by slug
	w = rec(r, "GET", "/api/v1/templates/slug/my-python-env", "", keyHdr(key))
	if w.Code != http.StatusOK {
		t.Fatalf("get template by slug: want 200, got %d: %s", w.Code, w.Body)
	}

	// Update
	w = rec(r, "PATCH", "/api/v1/templates/"+created.ID.String(),
		`{"description":"Python 3.12 with pytest and ruff"}`, keyHdr(key))
	if w.Code != http.StatusOK {
		t.Fatalf("update template: want 200, got %d: %s", w.Code, w.Body)
	}

	// Delete
	w = rec(r, "DELETE", "/api/v1/templates/"+created.ID.String(), "", keyHdr(key))
	if w.Code != http.StatusOK && w.Code != http.StatusNoContent {
		t.Fatalf("delete template: want 200/204, got %d: %s", w.Code, w.Body)
	}

	w = rec(r, "GET", "/api/v1/templates/"+created.ID.String(), "", keyHdr(key))
	if w.Code != http.StatusNotFound {
		t.Errorf("get deleted template: want 404, got %d", w.Code)
	}
}

// TestTemplateRejectsDuplicateSlug: slugs are used as URL path segments and
// must be unique.
func TestTemplateRejectsDuplicateSlug(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	key, principal := issueKey(t, r, "dana")
	seedOrgWithMember(t, "dana-org", principal, "admin")

	body := `{
		"slug": "duplicate-me",
		"name": "First",
		"description": "first template",
		"category": "testing",
		"image": "python:3.12-slim",
		"resources": {"cpu_limit": 1, "memory_limit_mb": 512, "timeout_seconds": 600}
	}`

	if w := rec(r, "POST", "/api/v1/templates", body, keyHdr(key)); w.Code != http.StatusCreated {
		t.Fatalf("first create: want 201, got %d: %s", w.Code, w.Body)
	}

	w := rec(r, "POST", "/api/v1/templates", body, keyHdr(key))
	if w.Code != http.StatusConflict {
		t.Errorf("duplicate slug: want 409, got %d: %s", w.Code, w.Body)
	}
}

// TestTemplateRejectsMaliciousInput drives the validation added in
// internal/templates/validate.go through the HTTP layer, confirming it returns
// 400 rather than 500 and that nothing reaches the database.
func TestTemplateRejectsMaliciousInput(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	key, principal := issueKey(t, r, "erin")
	seedOrgWithMember(t, "erin-org", principal, "admin")

	cases := []struct {
		name string
		body string
	}{
		{
			name: "file:// image",
			body: `{"slug":"bad-image","name":"Bad","description":"d","category":"c",
			        "image":"file:///etc/passwd",
			        "resources":{"cpu_limit":1,"memory_limit_mb":512,"timeout_seconds":600}}`,
		},
		{
			name: "shell metacharacters in image",
			body: `{"slug":"bad-image2","name":"Bad","description":"d","category":"c",
			        "image":"python:3.12;curl evil.example.com",
			        "resources":{"cpu_limit":1,"memory_limit_mb":512,"timeout_seconds":600}}`,
		},
		{
			name: "path traversal slug",
			body: `{"slug":"../../etc/passwd","name":"Bad","description":"d","category":"c",
			        "image":"python:3.12-slim",
			        "resources":{"cpu_limit":1,"memory_limit_mb":512,"timeout_seconds":600}}`,
		},
		{
			name: "negative cpu limit",
			body: `{"slug":"bad-cpu","name":"Bad","description":"d","category":"c",
			        "image":"python:3.12-slim",
			        "resources":{"cpu_limit":-1,"memory_limit_mb":512,"timeout_seconds":600}}`,
		},
		{
			name: "absurd memory limit",
			body: `{"slug":"bad-mem","name":"Bad","description":"d","category":"c",
			        "image":"python:3.12-slim",
			        "resources":{"cpu_limit":1,"memory_limit_mb":999999999,"timeout_seconds":600}}`,
		},
		{
			name: "wildcard allowed host",
			body: `{"slug":"bad-host","name":"Bad","description":"d","category":"c",
			        "image":"python:3.12-slim",
			        "resources":{"cpu_limit":1,"memory_limit_mb":512,"timeout_seconds":600},
			        "network":{"enabled":true,"allowed_hosts":["*"]}}`,
		},
		{
			name: "invalid env var name",
			body: `{"slug":"bad-env","name":"Bad","description":"d","category":"c",
			        "image":"python:3.12-slim",
			        "resources":{"cpu_limit":1,"memory_limit_mb":512,"timeout_seconds":600},
			        "environment":{"1INVALID":"x"}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := rec(r, "POST", "/api/v1/templates", tc.body, keyHdr(key))
			if w.Code != http.StatusBadRequest {
				t.Errorf("want 400, got %d: %s", w.Code, w.Body)
			}
		})
	}

	// Nothing should have been persisted.
	var count int
	if err := testDB.Get(&count, `SELECT COUNT(*) FROM session_templates`); err != nil {
		t.Fatalf("count templates: %v", err)
	}
	if count != 0 {
		t.Errorf("%d templates persisted despite validation failures, want 0", count)
	}
}

// TestTemplateBootstrapIsIdempotent: Bootstrap runs on every API start, so a
// restart must not duplicate or fail on the built-in templates.
func TestTemplateBootstrapIsIdempotent(t *testing.T) {
	truncateFeatureTables(t)
	_, _, mgr := featureRouter(t, testDB)

	for i := 1; i <= 3; i++ {
		if err := mgr.Bootstrap(t.Context()); err != nil {
			t.Fatalf("Bootstrap call %d: %v", i, err)
		}
	}

	var count int
	if err := testDB.Get(&count, `SELECT COUNT(*) FROM session_templates`); err != nil {
		t.Fatalf("count templates: %v", err)
	}
	if count != len(templates.BuiltInTemplates) {
		t.Errorf("after 3 Bootstrap calls there are %d templates, want %d",
			count, len(templates.BuiltInTemplates))
	}
}

func TestTemplateRequiresAuthentication(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	id := uuid.New().String()
	routes := []struct {
		method string
		path   string
	}{
		{"GET", "/api/v1/templates"},
		{"GET", "/api/v1/templates/" + id},
		{"GET", "/api/v1/templates/slug/python-data-science"},
		{"POST", "/api/v1/templates"},
		{"PATCH", "/api/v1/templates/" + id},
		{"DELETE", "/api/v1/templates/" + id},
	}

	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			w := rec(r, rt.method, rt.path, "{}", nil)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("want 401, got %d", w.Code)
			}
		})
	}
}

// ── Cost Intelligence ────────────────────────────────────────────────────────

// TestCostMetricsAreImmutable verifies the database trigger that blocks updates
// and deletes on cost_metrics. Billing records must be append-only.
func TestCostMetricsAreImmutable(t *testing.T) {
	truncateFeatureTables(t)
	_, _, _ = featureRouter(t, testDB)

	sessionID := seedFeatureSession(t, "master", nil, false)

	metricID := uuid.New()
	_, err := testDB.Exec(`
		INSERT INTO cost_metrics (
			id, session_id, period_start, period_end,
			cpu_core_hours, memory_gb_hours, egress_gb,
			compute_cost, storage_cost, network_cost, total_cost,
			checksum, signature
		) VALUES ($1, $2, NOW() - INTERVAL '1 hour', NOW(),
		          1.5, 4.0, 2.0, 0.06, 0.003, 0.18, 0.243,
		          'test-checksum', 'test-signature')
	`, metricID, sessionID)
	if err != nil {
		t.Fatalf("insert cost metric: %v", err)
	}

	t.Run("update blocked", func(t *testing.T) {
		_, err := testDB.Exec(`UPDATE cost_metrics SET total_cost = 0 WHERE id = $1`, metricID)
		if err == nil {
			t.Error("UPDATE on cost_metrics succeeded, want it blocked by the immutability trigger")
		}
	})

	t.Run("delete blocked", func(t *testing.T) {
		_, err := testDB.Exec(`DELETE FROM cost_metrics WHERE id = $1`, metricID)
		if err == nil {
			t.Error("DELETE on cost_metrics succeeded, want it blocked by the immutability trigger")
		}
	})

	// The row must still be there and unchanged.
	var total float64
	if err := testDB.Get(&total, `SELECT total_cost FROM cost_metrics WHERE id = $1`, metricID); err != nil {
		t.Fatalf("read back cost metric: %v", err)
	}
	if total != 0.243 {
		t.Errorf("total_cost = %v after blocked update, want 0.243", total)
	}
}

func TestCostRequiresAuthentication(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	w := rec(r, "GET", "/api/v1/costs/sessions/"+uuid.New().String(), "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
}

// TestCostCrossTenantIsolation: one tenant must not read another's costs.
func TestCostCrossTenantIsolation(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	aliceKey, alice := issueKey(t, r, "alice")
	bobKey, bob := issueKey(t, r, "bob")

	orgA := seedOrgWithMember(t, "org-a", alice, "admin")
	seedOrgWithMember(t, "org-b", bob, "admin")

	sessionA := seedFeatureSession(t, alice, &orgA, false)

	// Alice can read her own session's cost.
	w := rec(r, "GET", "/api/v1/costs/sessions/"+sessionA.String(), "", keyHdr(aliceKey))
	if w.Code != http.StatusOK {
		t.Fatalf("alice read own cost: want 200, got %d: %s", w.Code, w.Body)
	}

	// Bob must not.
	w = rec(r, "GET", "/api/v1/costs/sessions/"+sessionA.String(), "", keyHdr(bobKey))
	if w.Code == http.StatusOK {
		t.Errorf("bob read alice's session cost: got 200, body %s", w.Body)
	}
}

// ── Cross-cutting ────────────────────────────────────────────────────────────

// TestNewEndpointsRejectOversizedBodies: without a body limit, an authenticated
// caller can exhaust memory by posting a huge JSON document.
func TestNewEndpointsRejectOversizedBodies(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	key, principal := issueKey(t, r, "frank")
	seedOrgWithMember(t, "frank-org", principal, "admin")
	sessionID := seedFeatureSession(t, principal, nil, true)

	// A 10 MB description.
	huge := strings.Repeat("a", 10*1024*1024)

	t.Run("checkpoint description", func(t *testing.T) {
		body := fmt.Sprintf(`{"description":%q}`, huge)
		w := rec(r, "POST", "/api/v1/sessions/"+sessionID.String()+"/checkpoints", body, keyHdr(key))
		if w.Code == http.StatusCreated {
			t.Error("10 MB checkpoint description accepted, want it rejected")
		}
	})

	t.Run("template startup script", func(t *testing.T) {
		body := fmt.Sprintf(`{"slug":"huge","name":"Huge","description":"d","category":"c",
		                      "image":"python:3.12-slim",
		                      "resources":{"cpu_limit":1,"memory_limit_mb":512,"timeout_seconds":600},
		                      "startup_script":%q}`, huge)
		w := rec(r, "POST", "/api/v1/templates", body, keyHdr(key))
		if w.Code == http.StatusCreated {
			t.Error("10 MB startup script accepted, want it rejected")
		}
	})
}

// TestNewEndpointsSurviveMalformedJSON: a parse failure must be a 400, never a
// panic or a 500.
func TestNewEndpointsSurviveMalformedJSON(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	key, principal := issueKey(t, r, "grace")
	seedOrgWithMember(t, "grace-org", principal, "admin")
	sessionID := seedFeatureSession(t, principal, nil, true)

	bodies := []struct {
		name string
		body string
	}{
		{"truncated object", `{"description":`},
		{"array instead of object", `["description"]`},
		{"bare string", `"description"`},
		{"null", `null`},
		{"deeply nested", strings.Repeat(`{"a":`, 200) + `1` + strings.Repeat(`}`, 200)},
		{"wrong types", `{"description": 12345, "name": true}`},
		{"duplicate keys", `{"description":"a","description":"b"}`},
		{"unicode escape", `{"description":"\ud800"}`},
	}

	endpoints := []struct {
		name string
		path string
	}{
		{"checkpoint", "/api/v1/sessions/" + sessionID.String() + "/checkpoints"},
		{"template", "/api/v1/templates"},
	}

	for _, ep := range endpoints {
		for _, b := range bodies {
			t.Run(ep.name+"/"+b.name, func(t *testing.T) {
				w := rec(r, "POST", ep.path, b.body, keyHdr(key))
				if w.Code >= 500 {
					t.Errorf("POST %s with %s returned %d, want a 4xx: %s",
						ep.path, b.name, w.Code, w.Body)
				}
			})
		}
	}
}

// TestAuditTrailRecordsFeatureActions: every new mutating endpoint must leave an
// audit entry, otherwise the compliance story for these features is incomplete.
func TestAuditTrailRecordsFeatureActions(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	key, principal := issueKey(t, r, "heidi")
	seedOrgWithMember(t, "heidi-org", principal, "admin")
	sessionID := seedFeatureSession(t, principal, nil, true)

	// Create a checkpoint and a template.
	w := rec(r, "POST", "/api/v1/sessions/"+sessionID.String()+"/checkpoints",
		`{"description":"audited"}`, keyHdr(key))
	if w.Code != http.StatusCreated {
		t.Fatalf("create checkpoint: want 201, got %d: %s", w.Code, w.Body)
	}

	w = rec(r, "POST", "/api/v1/templates", `{
		"slug":"audited-template","name":"Audited","description":"d","category":"c",
		"image":"python:3.12-slim",
		"resources":{"cpu_limit":1,"memory_limit_mb":512,"timeout_seconds":600}
	}`, keyHdr(key))
	if w.Code != http.StatusCreated {
		t.Fatalf("create template: want 201, got %d: %s", w.Code, w.Body)
	}

	wantActions := []string{"checkpoint.created", "template.created"}
	for _, action := range wantActions {
		var count int
		err := testDB.Get(&count,
			`SELECT COUNT(*) FROM audit_logs WHERE action = $1`, action)
		if err != nil {
			t.Fatalf("count audit logs for %s: %v", action, err)
		}
		if count == 0 {
			t.Errorf("no audit entry recorded for %q", action)
		}
	}
}
