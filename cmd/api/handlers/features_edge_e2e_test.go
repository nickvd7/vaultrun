//go:build integration

package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/nickvd7/vaultrun/internal/config"
	"github.com/nickvd7/vaultrun/internal/cost"
	"github.com/nickvd7/vaultrun/internal/templates"
)

// ── shared helpers ───────────────────────────────────────────────────────────

// addOrgMember adds a principal to an existing org.
func addOrgMember(t *testing.T, orgID uuid.UUID, principal, role string) {
	t.Helper()

	_, err := testDB.Exec(
		`INSERT INTO org_members (org_id, principal, role) VALUES ($1, $2, $3)`,
		orgID, principal, role)
	if err != nil {
		t.Fatalf("add org member %s/%s: %v", principal, role, err)
	}
}

// recordCostMetric bills a session for a fixed amount of usage through the real
// tracker, so the row carries a valid checksum and signature.
func recordCostMetric(t *testing.T, sessionID uuid.UUID, cpuHours float64) {
	t.Helper()

	tracker := cost.New(testDB, []byte(testHMACKey))
	start := time.Now().UTC().Truncate(time.Hour)
	err := tracker.RecordMetric(context.Background(), &cost.CostMetric{
		ID:           uuid.New(),
		SessionID:    sessionID,
		PeriodStart:  start,
		PeriodEnd:    start.Add(time.Hour),
		CPUCoreHours: cpuHours,
		CreatedAt:    time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("record cost metric: %v", err)
	}
}

// seedAlert inserts a cost alert attached to a session, an org, or neither.
func seedAlert(t *testing.T, sessionID, orgID *uuid.UUID, title string) uuid.UUID {
	t.Helper()

	id := uuid.New()
	_, err := testDB.Exec(`
		INSERT INTO cost_alerts (id, alert_type, severity, session_id, org_id, title, description)
		VALUES ($1, 'idle_session', 'warning', $2, $3, $4, 'seeded by test')
	`, id, sessionID, orgID, title)
	if err != nil {
		t.Fatalf("seed alert: %v", err)
	}
	return id
}

// createTemplate inserts a template through the manager, which applies the same
// validation the API does.
func createTemplate(t *testing.T, mgr *templates.Manager, orgID *uuid.UUID, slug string, published bool, tweaks ...func(*templates.CreateTemplateRequest)) *templates.Template {
	t.Helper()

	req := templates.CreateTemplateRequest{
		Slug:        slug,
		Name:        "Template " + slug,
		Description: "seeded by test",
		Category:    "testing",
		Image:       "python:3.12-slim",
		Resources: templates.ResourceConfig{
			CPULimit:       1,
			MemoryLimitMB:  512,
			TimeoutSeconds: 600,
		},
	}
	for _, tweak := range tweaks {
		tweak(&req)
	}

	tmpl, err := mgr.Create(context.Background(), orgID, req)
	if err != nil {
		t.Fatalf("create template %s: %v", slug, err)
	}

	if published {
		yes := true
		if _, err := mgr.Update(context.Background(), tmpl.ID, templates.UpdateTemplateRequest{Published: &yes}); err != nil {
			t.Fatalf("publish template %s: %v", slug, err)
		}
		tmpl.Published = true
	}
	return tmpl
}

// ── Cost Intelligence: tenant scoping ────────────────────────────────────────

// TestCostBreakdownIsScopedToCaller: the deployment-wide breakdown used to be
// returned to every authenticated key, exposing total spend plus the names and
// IDs of the ten highest-spending sessions across all tenants.
func TestCostBreakdownIsScopedToCaller(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	aliceKey, alice := issueKey(t, r, "alice")
	bobKey, bob := issueKey(t, r, "bob")

	aliceSession := seedFeatureSession(t, alice, nil, false)
	bobSession := seedFeatureSession(t, bob, nil, false)

	recordCostMetric(t, aliceSession, 1)  // cheap
	recordCostMetric(t, bobSession, 1000) // expensive

	period := time.Now().UTC().Format("2006-01")

	readBreakdown := func(hdr map[string]string) cost.CostBreakdown {
		w := rec(r, "GET", "/api/v1/costs/breakdown?period="+period, "", hdr)
		if w.Code != http.StatusOK {
			t.Fatalf("breakdown: want 200, got %d: %s", w.Code, w.Body)
		}
		var out cost.CostBreakdown
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode breakdown: %v", err)
		}
		return out
	}

	aliceView := readBreakdown(keyHdr(aliceKey))
	bobView := readBreakdown(keyHdr(bobKey))
	masterView := readBreakdown(masterHdr())

	if aliceView.TotalCost == 0 {
		t.Fatal("alice sees no spend at all — the scope filter dropped her own session too")
	}
	if aliceView.TotalCost >= bobView.TotalCost {
		t.Errorf("alice total %.4f is not below bob's %.4f — she is seeing his spend",
			aliceView.TotalCost, bobView.TotalCost)
	}
	for _, s := range aliceView.TopSessions {
		if s.SessionID == bobSession {
			t.Error("alice's top-sessions list names bob's session")
		}
	}
	for _, s := range bobView.TopSessions {
		if s.SessionID == aliceSession {
			t.Error("bob's top-sessions list names alice's session")
		}
	}

	// The master key keeps the deployment-wide view it is meant to have.
	if masterView.TotalCost < aliceView.TotalCost+bobView.TotalCost {
		t.Errorf("master total %.4f is below the sum of both tenants (%.4f) — the scope leaked into the master path",
			masterView.TotalCost, aliceView.TotalCost+bobView.TotalCost)
	}
	if len(masterView.TopSessions) != 2 {
		t.Errorf("master sees %d top sessions, want both", len(masterView.TopSessions))
	}
}

// TestCostBreakdownIncludesOrgSessions: scoping must follow the same visibility
// rule as the rest of the API — a member sees their org's sessions, not only
// the ones they created.
func TestCostBreakdownIncludesOrgSessions(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	ownerKey, owner := issueKey(t, r, "owner")
	viewerKey, viewer := issueKey(t, r, "viewer")

	orgID := seedOrgWithMember(t, "acme", owner, "admin")
	addOrgMember(t, orgID, viewer, "viewer")

	orgSession := seedFeatureSession(t, owner, &orgID, false)
	recordCostMetric(t, orgSession, 5)

	_ = ownerKey
	w := rec(r, "GET", "/api/v1/costs/breakdown?period="+time.Now().UTC().Format("2006-01"), "", keyHdr(viewerKey))
	if w.Code != http.StatusOK {
		t.Fatalf("breakdown: want 200, got %d: %s", w.Code, w.Body)
	}
	var out cost.CostBreakdown
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode breakdown: %v", err)
	}
	if out.TotalCost == 0 {
		t.Error("an org member sees no spend for their org's session")
	}
}

// TestCostAlertsAreScopedToCaller covers both reading and resolving. Resolving
// another tenant's alert would let one tenant hide a budget warning from
// another.
func TestCostAlertsAreScopedToCaller(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	aliceKey, alice := issueKey(t, r, "alice")
	bobKey, bob := issueKey(t, r, "bob")

	aliceSession := seedFeatureSession(t, alice, nil, false)
	bobSession := seedFeatureSession(t, bob, nil, false)

	aliceAlert := seedAlert(t, &aliceSession, nil, "alice idle session")
	bobAlert := seedAlert(t, &bobSession, nil, "bob idle session")
	globalAlert := seedAlert(t, nil, nil, "deployment notice")

	listTitles := func(hdr map[string]string) []string {
		w := rec(r, "GET", "/api/v1/costs/alerts", "", hdr)
		if w.Code != http.StatusOK {
			t.Fatalf("list alerts: want 200, got %d: %s", w.Code, w.Body)
		}
		var out struct {
			Alerts []cost.CostAlert `json:"alerts"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode alerts: %v", err)
		}
		titles := make([]string, 0, len(out.Alerts))
		for _, a := range out.Alerts {
			titles = append(titles, a.Title)
		}
		return titles
	}

	aliceTitles := listTitles(keyHdr(aliceKey))
	if len(aliceTitles) != 1 || aliceTitles[0] != "alice idle session" {
		t.Errorf("alice sees alerts %v, want only her own", aliceTitles)
	}

	masterTitles := listTitles(masterHdr())
	if len(masterTitles) != 3 {
		t.Errorf("master sees %d alerts, want all 3", len(masterTitles))
	}

	// Resolving someone else's alert is indistinguishable from resolving one
	// that does not exist.
	w := rec(r, "POST", "/api/v1/costs/alerts/"+bobAlert.String()+"/resolve", "", keyHdr(aliceKey))
	if w.Code != http.StatusNotFound {
		t.Errorf("alice resolving bob's alert: want 404, got %d: %s", w.Code, w.Body)
	}
	w = rec(r, "POST", "/api/v1/costs/alerts/"+globalAlert.String()+"/resolve", "", keyHdr(aliceKey))
	if w.Code != http.StatusNotFound {
		t.Errorf("alice resolving a deployment-wide alert: want 404, got %d: %s", w.Code, w.Body)
	}
	w = rec(r, "POST", "/api/v1/costs/alerts/"+uuid.New().String()+"/resolve", "", keyHdr(aliceKey))
	if w.Code != http.StatusNotFound {
		t.Errorf("resolving an unknown alert: want 404, got %d: %s", w.Code, w.Body)
	}

	// Bob's alert must still be unresolved after alice's attempt.
	var resolved bool
	if err := testDB.Get(&resolved, `SELECT resolved FROM cost_alerts WHERE id = $1`, bobAlert); err != nil {
		t.Fatalf("read alert state: %v", err)
	}
	if resolved {
		t.Error("alice's rejected request still resolved bob's alert")
	}

	// The owner can resolve their own.
	w = rec(r, "POST", "/api/v1/costs/alerts/"+aliceAlert.String()+"/resolve", "", keyHdr(aliceKey))
	if w.Code != http.StatusOK {
		t.Errorf("alice resolving her own alert: want 200, got %d: %s", w.Code, w.Body)
	}
	_ = bobKey
}

// TestOrgCostSummaryReturnsData is a regression test for two defects that made
// the endpoint useless: the summary query named a table that does not exist
// (orgs rather than organizations), so every authorised request answered 500,
// and the budget was scanned into a pointer-to-pointer so it never came back.
func TestOrgCostSummaryReturnsData(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	adminKey, admin := issueKey(t, r, "admin")
	orgID := seedOrgWithMember(t, "acme", admin, "admin")
	session := seedFeatureSession(t, admin, &orgID, false)
	recordCostMetric(t, session, 3)

	month := time.Now().UTC().Format("2006-01")

	w := rec(r, "POST", "/api/v1/orgs/"+orgID.String()+"/budget",
		`{"monthly_limit":500,"alert_threshold":0.75}`, keyHdr(adminKey))
	if w.Code != http.StatusOK {
		t.Fatalf("set budget: want 200, got %d: %s", w.Code, w.Body)
	}

	w = rec(r, "GET", "/api/v1/orgs/"+orgID.String()+"/costs?month="+month, "", keyHdr(adminKey))
	if w.Code != http.StatusOK {
		t.Fatalf("org costs: want 200, got %d: %s", w.Code, w.Body)
	}

	var out struct {
		Summary *cost.OrgCostSummary `json:"summary"`
		Budget  *cost.CostBudget     `json:"budget"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode org costs: %v", err)
	}

	if out.Summary == nil {
		t.Fatal("org cost response has no summary")
	}
	if out.Summary.OrgID != orgID {
		t.Errorf("summary org_id = %s, want %s", out.Summary.OrgID, orgID)
	}
	if out.Summary.TotalCost == 0 {
		t.Error("summary total cost is zero although the org has a billed session")
	}
	if out.Summary.SessionCount != 1 {
		t.Errorf("summary session_count = %d, want 1", out.Summary.SessionCount)
	}
	if out.Budget == nil {
		t.Fatal("org cost response has no budget although one was just set")
	}
	if out.Budget.MonthlyLimit != 500 {
		t.Errorf("budget monthly_limit = %.2f, want 500", out.Budget.MonthlyLimit)
	}
}

// TestBudgetRejectsUnusableInput exercises the API surface of the validation
// covered by unit tests, and confirms nothing is written on rejection.
func TestBudgetRejectsUnusableInput(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	adminKey, admin := issueKey(t, r, "admin")
	orgID := seedOrgWithMember(t, "acme", admin, "admin")

	cases := []struct {
		name string
		body string
	}{
		{"negative limit", `{"monthly_limit":-100}`},
		{"threshold above one", `{"monthly_limit":100,"alert_threshold":5}`},
		{"negative threshold", `{"monthly_limit":100,"alert_threshold":-0.5}`},
		{"limit beyond column precision", `{"monthly_limit":1e12}`},
		{"malformed month", `{"monthly_limit":100,"month":"july"}`},
		{"missing limit", `{"alert_threshold":0.5}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := rec(r, "POST", "/api/v1/orgs/"+orgID.String()+"/budget", tc.body, keyHdr(adminKey))
			if w.Code != http.StatusBadRequest {
				t.Errorf("want 400, got %d: %s", w.Code, w.Body)
			}
		})
	}

	var count int
	if err := testDB.Get(&count, `SELECT COUNT(*) FROM cost_budgets WHERE org_id = $1`, orgID); err != nil {
		t.Fatalf("count budgets: %v", err)
	}
	if count != 0 {
		t.Errorf("%d budget rows were written despite every request being rejected", count)
	}
}

// TestBudgetRequiresOrgAdmin: a viewer or executor must not be able to raise
// their org's spending limit.
func TestBudgetRequiresOrgAdmin(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	adminKey, admin := issueKey(t, r, "admin")
	viewerKey, viewer := issueKey(t, r, "viewer")
	execKey, executor := issueKey(t, r, "executor")
	outsiderKey, _ := issueKey(t, r, "outsider")

	orgID := seedOrgWithMember(t, "acme", admin, "admin")
	addOrgMember(t, orgID, viewer, "viewer")
	addOrgMember(t, orgID, executor, "executor")

	body := `{"monthly_limit":10000}`

	for _, tc := range []struct {
		name string
		key  string
		want int
	}{
		{"admin", adminKey, http.StatusOK},
		{"executor", execKey, http.StatusForbidden},
		{"viewer", viewerKey, http.StatusForbidden},
		{"non-member", outsiderKey, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := rec(r, "POST", "/api/v1/orgs/"+orgID.String()+"/budget", body, keyHdr(tc.key))
			if w.Code != tc.want {
				t.Errorf("want %d, got %d: %s", tc.want, w.Code, w.Body)
			}
		})
	}
}

// TestCostBreakdownRejectsMalformedPeriod: the period is concatenated into a
// timestamp literal, so it must be validated before it reaches the query.
func TestCostBreakdownRejectsMalformedPeriod(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	for _, period := range []string{
		"2026-13", "202607", "july", "2026-07-01", "-1",
		"2026-07%27%3B%20DROP%20TABLE%20cost_metrics--",
		strings.Repeat("9", 100),
	} {
		w := rec(r, "GET", "/api/v1/costs/breakdown?period="+period, "", masterHdr())
		if w.Code != http.StatusBadRequest {
			t.Errorf("period %q: want 400, got %d: %s", period, w.Code, w.Body)
		}
	}
}

// ── Session Templates: ownership ─────────────────────────────────────────────

// TestTemplateUpdateRequiresOwnership is the highest-impact access-control gap
// found: update and delete carried a TODO instead of a check, so any
// authenticated key could repoint any template — including a built-in — at an
// image of its choosing, and every session later created from that template
// would run it.
func TestTemplateUpdateRequiresOwnership(t *testing.T) {
	truncateFeatureTables(t)
	r, _, mgr := featureRouter(t, testDB)

	ownerKey, owner := issueKey(t, r, "owner")
	memberKey, member := issueKey(t, r, "member")
	strangerKey, stranger := issueKey(t, r, "stranger")

	ownerOrg := seedOrgWithMember(t, "acme", owner, "admin")
	addOrgMember(t, ownerOrg, member, "executor")
	seedOrgWithMember(t, "other", stranger, "admin")

	tmpl := createTemplate(t, mgr, &ownerOrg, "team-template", true)
	builtIn := createTemplate(t, mgr, nil, "builtin-template", true)

	hijack := `{"image":"attacker/backdoored:latest"}`

	for _, tc := range []struct {
		name     string
		key      string
		template uuid.UUID
		want     int
	}{
		{"another org's admin", strangerKey, tmpl.ID, http.StatusForbidden},
		{"own org, executor not admin", memberKey, tmpl.ID, http.StatusForbidden},
		{"built-in via org admin", ownerKey, builtIn.ID, http.StatusForbidden},
		{"own org admin", ownerKey, tmpl.ID, http.StatusOK},
		{"built-in via master", testMasterKey, builtIn.ID, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := rec(r, "PATCH", "/api/v1/templates/"+tc.template.String(), hijack, keyHdr(tc.key))
			if w.Code != tc.want {
				t.Errorf("want %d, got %d: %s", tc.want, w.Code, w.Body)
			}
		})
	}

	// The rejected attempts must not have changed the image.
	stored, err := mgr.Get(context.Background(), tmpl.ID)
	if err != nil {
		t.Fatalf("reload template: %v", err)
	}
	if stored.Image != "attacker/backdoored:latest" {
		t.Errorf("owner's own update did not apply; image = %q", stored.Image)
	}
}

// TestTemplateDeleteRequiresOwnership: deleting a shared template is as
// disruptive as rewriting it, so it needs the same check.
func TestTemplateDeleteRequiresOwnership(t *testing.T) {
	truncateFeatureTables(t)
	r, _, mgr := featureRouter(t, testDB)

	ownerKey, owner := issueKey(t, r, "owner")
	strangerKey, stranger := issueKey(t, r, "stranger")

	ownerOrg := seedOrgWithMember(t, "acme", owner, "admin")
	seedOrgWithMember(t, "other", stranger, "admin")

	tmpl := createTemplate(t, mgr, &ownerOrg, "team-template", true)
	builtIn := createTemplate(t, mgr, nil, "builtin-template", true)

	w := rec(r, "DELETE", "/api/v1/templates/"+tmpl.ID.String(), "", keyHdr(strangerKey))
	if w.Code != http.StatusForbidden {
		t.Errorf("stranger deleting another org's template: want 403, got %d: %s", w.Code, w.Body)
	}
	w = rec(r, "DELETE", "/api/v1/templates/"+builtIn.ID.String(), "", keyHdr(ownerKey))
	if w.Code != http.StatusForbidden {
		t.Errorf("org admin deleting a built-in: want 403, got %d: %s", w.Code, w.Body)
	}

	if _, err := mgr.Get(context.Background(), tmpl.ID); err != nil {
		t.Errorf("template was deleted despite the request being refused: %v", err)
	}
	if _, err := mgr.Get(context.Background(), builtIn.ID); err != nil {
		t.Errorf("built-in template was deleted despite the request being refused: %v", err)
	}

	w = rec(r, "DELETE", "/api/v1/templates/"+tmpl.ID.String(), "", keyHdr(ownerKey))
	if w.Code != http.StatusOK {
		t.Errorf("owner deleting their own template: want 200, got %d: %s", w.Code, w.Body)
	}
}

// TestUnpublishedTemplateIsHiddenFromOtherOrgs: the list endpoint filters drafts
// out for non-master callers, but a direct lookup by ID or slug did not — so
// another org's draft image, env keys and startup script were readable.
func TestUnpublishedTemplateIsHiddenFromOtherOrgs(t *testing.T) {
	truncateFeatureTables(t)
	r, _, mgr := featureRouter(t, testDB)

	ownerKey, owner := issueKey(t, r, "owner")
	strangerKey, stranger := issueKey(t, r, "stranger")

	ownerOrg := seedOrgWithMember(t, "acme", owner, "admin")
	seedOrgWithMember(t, "other", stranger, "admin")

	draft := createTemplate(t, mgr, &ownerOrg, "secret-draft", false)
	published := createTemplate(t, mgr, &ownerOrg, "public-template", true)

	for _, tc := range []struct {
		name string
		path string
		key  string
		want int
	}{
		{"stranger reads draft by id", "/api/v1/templates/" + draft.ID.String(), strangerKey, http.StatusNotFound},
		{"stranger reads draft by slug", "/api/v1/templates/slug/" + draft.Slug, strangerKey, http.StatusNotFound},
		{"author org reads its own draft", "/api/v1/templates/" + draft.ID.String(), ownerKey, http.StatusOK},
		{"master reads any draft", "/api/v1/templates/" + draft.ID.String(), testMasterKey, http.StatusOK},
		{"stranger reads published", "/api/v1/templates/" + published.ID.String(), strangerKey, http.StatusOK},
		{"stranger reads published by slug", "/api/v1/templates/slug/" + published.Slug, strangerKey, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := rec(r, "GET", tc.path, "", keyHdr(tc.key))
			if w.Code != tc.want {
				t.Errorf("want %d, got %d: %s", tc.want, w.Code, w.Body)
			}
		})
	}
}

// TestTemplateSessionEnforcesImageAllowlist: creating a session from a template
// used to skip every gate POST /sessions applies. The image allowlist is the
// most serious of the three — it is what stops a caller from running an
// arbitrary, untrusted image in the sandbox.
func TestTemplateSessionEnforcesImageAllowlist(t *testing.T) {
	truncateFeatureTables(t)
	r, _, mgr := featureRouter(t, testDB, func(c *config.Config) {
		c.Docker.ImageAllowlist = []string{"python:3.12-slim"}
	})

	key, principal := issueKey(t, r, "alice")
	org := seedOrgWithMember(t, "acme", principal, "admin")

	allowed := createTemplate(t, mgr, &org, "allowed-image", true)
	denied := createTemplate(t, mgr, &org, "denied-image", true, func(req *templates.CreateTemplateRequest) {
		req.Image = "attacker/privileged:latest"
	})

	w := rec(r, "POST", "/api/v1/templates/"+denied.ID.String()+"/use", `{}`, keyHdr(key))
	if w.Code != http.StatusBadRequest {
		t.Errorf("template with a disallowed image: want 400, got %d: %s", w.Code, w.Body)
	}

	var count int
	if err := testDB.Get(&count, `SELECT COUNT(*) FROM sessions WHERE image = $1`, "attacker/privileged:latest"); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if count != 0 {
		t.Errorf("%d sessions were created with the disallowed image", count)
	}

	w = rec(r, "POST", "/api/v1/templates/"+allowed.ID.String()+"/use", `{}`, keyHdr(key))
	if w.Code != http.StatusCreated {
		t.Errorf("template with an allowed image: want 201, got %d: %s", w.Code, w.Body)
	}
}

// TestTemplateSessionEnforcesResourceCeilings: a template could request more CPU
// or memory than the deployment permits and the session would be created with
// it, bypassing the ceiling POST /sessions enforces.
func TestTemplateSessionEnforcesResourceCeilings(t *testing.T) {
	t.Setenv("MAX_SESSION_CPU", "2")
	t.Setenv("MAX_SESSION_MEMORY_MB", "1024")
	t.Setenv("MAX_SESSION_TIMEOUT_SEC", "3600")

	truncateFeatureTables(t)
	r, _, mgr := featureRouter(t, testDB)

	key, principal := issueKey(t, r, "alice")
	org := seedOrgWithMember(t, "acme", principal, "admin")

	cases := []struct {
		name  string
		slug  string
		tweak func(*templates.CreateTemplateRequest)
	}{
		{"cpu above ceiling", "greedy-cpu", func(req *templates.CreateTemplateRequest) {
			req.Resources.CPULimit = 8
		}},
		{"memory above ceiling", "greedy-memory", func(req *templates.CreateTemplateRequest) {
			req.Resources.MemoryLimitMB = 8192
		}},
		{"timeout above ceiling", "greedy-timeout", func(req *templates.CreateTemplateRequest) {
			req.Resources.TimeoutSeconds = 86400
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpl := createTemplate(t, mgr, &org, tc.slug, true, tc.tweak)
			w := rec(r, "POST", "/api/v1/templates/"+tmpl.ID.String()+"/use", `{}`, keyHdr(key))
			if w.Code != http.StatusBadRequest {
				t.Errorf("want 400, got %d: %s", w.Code, w.Body)
			}
		})
	}

	// A template inside the ceilings still works.
	ok := createTemplate(t, mgr, &org, "modest", true, func(req *templates.CreateTemplateRequest) {
		req.Resources.CPULimit = 2
		req.Resources.MemoryLimitMB = 1024
		req.Resources.TimeoutSeconds = 3600
	})
	w := rec(r, "POST", "/api/v1/templates/"+ok.ID.String()+"/use", `{}`, keyHdr(key))
	if w.Code != http.StatusCreated {
		t.Errorf("template within the ceilings: want 201, got %d: %s", w.Code, w.Body)
	}
}

// TestTemplateSessionEnforcesQuota: without the quota check this endpoint was an
// unbounded session factory.
func TestTemplateSessionEnforcesQuota(t *testing.T) {
	t.Setenv("MAX_SESSIONS_PER_ACTOR", "2")

	truncateFeatureTables(t)
	r, _, mgr := featureRouter(t, testDB)

	key, principal := issueKey(t, r, "alice")
	org := seedOrgWithMember(t, "acme", principal, "admin")
	tmpl := createTemplate(t, mgr, &org, "quota-template", true)

	for i := 1; i <= 2; i++ {
		w := rec(r, "POST", "/api/v1/templates/"+tmpl.ID.String()+"/use", `{}`, keyHdr(key))
		if w.Code != http.StatusCreated {
			t.Fatalf("session %d: want 201, got %d: %s", i, w.Code, w.Body)
		}
	}

	w := rec(r, "POST", "/api/v1/templates/"+tmpl.ID.String()+"/use", `{}`, keyHdr(key))
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("session past the quota: want 429, got %d: %s", w.Code, w.Body)
	}

	// The master key stays exempt, matching POST /sessions.
	w = rec(r, "POST", "/api/v1/templates/"+tmpl.ID.String()+"/use", `{}`, masterHdr())
	if w.Code != http.StatusCreated {
		t.Errorf("master past the quota: want 201, got %d: %s", w.Code, w.Body)
	}
}

// TestTemplateSessionWorksWithoutOrg: a key that belongs to no org used to get
// a 500 from the org lookup. It now gets a personal session, and the usage row
// is still recorded so the marketplace counter stays accurate.
func TestTemplateSessionWorksWithoutOrg(t *testing.T) {
	truncateFeatureTables(t)
	r, _, mgr := featureRouter(t, testDB)

	key, _ := issueKey(t, r, "loner")
	tmpl := createTemplate(t, mgr, nil, "solo-template", true)

	w := rec(r, "POST", "/api/v1/templates/"+tmpl.ID.String()+"/use", `{"name":"my session"}`, keyHdr(key))
	if w.Code != http.StatusCreated {
		t.Fatalf("use template without an org: want 201, got %d: %s", w.Code, w.Body)
	}

	var out struct {
		Session struct {
			ID         uuid.UUID  `json:"id"`
			OrgID      *uuid.UUID `json:"org_id"`
			TemplateID *uuid.UUID `json:"template_id"`
		} `json:"session"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	if out.Session.OrgID != nil {
		t.Errorf("session org_id = %v, want null for an org-less caller", out.Session.OrgID)
	}
	if out.Session.TemplateID == nil || *out.Session.TemplateID != tmpl.ID {
		t.Errorf("session template_id = %v, want %s", out.Session.TemplateID, tmpl.ID)
	}

	reloaded, err := mgr.Get(context.Background(), tmpl.ID)
	if err != nil {
		t.Fatalf("reload template: %v", err)
	}
	if reloaded.UseCount != 1 {
		t.Errorf("template use_count = %d, want 1 — the usage row was dropped", reloaded.UseCount)
	}
}

// TestTemplateSessionRefusesHiddenTemplate: a draft belonging to another org
// must not be instantiable, or the visibility check on GET would be pointless.
func TestTemplateSessionRefusesHiddenTemplate(t *testing.T) {
	truncateFeatureTables(t)
	r, _, mgr := featureRouter(t, testDB)

	_, owner := issueKey(t, r, "owner")
	strangerKey, stranger := issueKey(t, r, "stranger")

	ownerOrg := seedOrgWithMember(t, "acme", owner, "admin")
	seedOrgWithMember(t, "other", stranger, "admin")

	draft := createTemplate(t, mgr, &ownerOrg, "secret-draft", false)

	w := rec(r, "POST", "/api/v1/templates/"+draft.ID.String()+"/use", `{}`, keyHdr(strangerKey))
	if w.Code != http.StatusNotFound {
		t.Errorf("using another org's draft: want 404, got %d: %s", w.Code, w.Body)
	}
}

// ── shared: concurrency ──────────────────────────────────────────────────────

// parallelRequests fires n identical requests at once and returns the status
// codes. Used to check invariants that only break under concurrency.
func parallelRequests(r *gin.Engine, n int, method, path, body string, hdr map[string]string) []int {
	var wg sync.WaitGroup
	codes := make([]int, n)

	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			codes[i] = rec(r, method, path, body, hdr).Code
		}(i)
	}
	close(start)
	wg.Wait()

	return codes
}

// countByCode summarises a slice of status codes.
func countByCode(codes []int) map[int]int {
	out := map[int]int{}
	for _, c := range codes {
		out[c]++
	}
	return out
}

var _ = fmt.Sprintf
