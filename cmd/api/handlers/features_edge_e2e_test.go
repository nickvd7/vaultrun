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
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/nickvd7/vaultrun/internal/config"
	"github.com/nickvd7/vaultrun/internal/cost"
	"github.com/nickvd7/vaultrun/internal/replay"
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

// ── Session Replay: fork, paging, previews ───────────────────────────────────

// makeCheckpoint creates a checkpoint over HTTP and returns its ID.
func makeCheckpoint(t *testing.T, r *gin.Engine, sessionID uuid.UUID, body string, hdr map[string]string) uuid.UUID {
	t.Helper()

	w := rec(r, "POST", "/api/v1/sessions/"+sessionID.String()+"/checkpoints", body, hdr)
	if w.Code != http.StatusCreated {
		t.Fatalf("create checkpoint: want 201, got %d: %s", w.Code, w.Body)
	}
	var out struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode checkpoint: %v", err)
	}
	return out.ID
}

// forkedSessionID reads the session id out of a fork response.
func forkedSessionID(t *testing.T, body []byte) uuid.UUID {
	t.Helper()

	var out struct {
		SessionID uuid.UUID `json:"session_id"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode fork response: %v", err)
	}
	if out.SessionID == uuid.Nil {
		t.Fatalf("fork response carries no session_id: %s", body)
	}
	return out.SessionID
}

// TestForkProducesAUsableSession: the fork endpoint used to answer 201 with a
// freshly generated UUID that matched no row, so callers received the id of a
// session that did not exist.
func TestForkProducesAUsableSession(t *testing.T) {
	truncateFeatureTables(t)
	r, stubWS, _ := featureRouter(t, testDB)

	key, principal := issueKey(t, r, "alice")
	org := seedOrgWithMember(t, "acme", principal, "admin")
	source := seedFeatureSession(t, principal, &org, true)

	cpID := makeCheckpoint(t, r, source, `{"description":"before the change"}`, keyHdr(key))

	restoresBefore := len(stubWS.restored)
	w := rec(r, "POST", "/api/v1/checkpoints/"+cpID.String()+"/fork",
		`{"name":"experiment"}`, keyHdr(key))
	if w.Code != http.StatusCreated {
		t.Fatalf("fork: want 201, got %d: %s", w.Code, w.Body)
	}
	forkID := forkedSessionID(t, w.Body.Bytes())

	var fork struct {
		Name                   *string    `db:"name"`
		Image                  string     `db:"image"`
		Status                 string     `db:"status"`
		CreatedBy              string     `db:"created_by"`
		OrgID                  *uuid.UUID `db:"org_id"`
		ReplayEnabled          bool       `db:"replay_enabled"`
		ForkedFromCheckpointID *uuid.UUID `db:"forked_from_checkpoint_id"`
		WorkspacePath          string     `db:"workspace_path"`
	}
	err := testDB.Get(&fork, `
		SELECT name, image, status, created_by, org_id, replay_enabled,
		       forked_from_checkpoint_id, workspace_path
		FROM sessions WHERE id = $1`, forkID)
	if err != nil {
		t.Fatalf("the fork's session row does not exist: %v", err)
	}

	if fork.ForkedFromCheckpointID == nil || *fork.ForkedFromCheckpointID != cpID {
		t.Errorf("fork's forked_from_checkpoint_id = %v, want %s", fork.ForkedFromCheckpointID, cpID)
	}
	if fork.Name == nil || *fork.Name != "experiment" {
		t.Errorf("fork name = %v, want \"experiment\"", fork.Name)
	}
	if fork.Image != "python:3.12-slim" {
		t.Errorf("fork image = %q, want the source session's", fork.Image)
	}
	if fork.OrgID == nil || *fork.OrgID != org {
		t.Errorf("fork org_id = %v, want %s", fork.OrgID, org)
	}
	if !fork.ReplayEnabled {
		t.Error("fork did not inherit replay_enabled, so it cannot be checkpointed in turn")
	}
	if fork.Status != "created" {
		t.Errorf("fork status = %q, want \"created\" — it has no container yet", fork.Status)
	}
	if fork.WorkspacePath == "" {
		t.Error("fork has no workspace path")
	}

	// The snapshot must actually be unpacked into the new workspace, otherwise
	// the fork reproduces nothing.
	if len(stubWS.restored) != restoresBefore+1 {
		t.Errorf("workspace restores = %d, want %d — the snapshot was not unpacked into the fork",
			len(stubWS.restored), restoresBefore+1)
	}

	// The source session must be untouched.
	var srcForked *uuid.UUID
	if err := testDB.Get(&srcForked,
		`SELECT forked_from_checkpoint_id FROM sessions WHERE id = $1`, source); err != nil {
		t.Fatalf("reload source session: %v", err)
	}
	if srcForked != nil {
		t.Error("forking rewrote the source session")
	}
}

// TestForkIsAttributedToTheCaller: the fork copied created_by from the source
// session, so a fork of a colleague's checkpoint landed under the colleague's
// name. Two consequences: the audit trail names the wrong actor, and the quota
// check — which counts the caller's own sessions — never sees the rows the
// caller is creating, making fork an unbounded session factory.
func TestForkIsAttributedToTheCaller(t *testing.T) {
	t.Setenv("MAX_SESSIONS_PER_ACTOR", "2")

	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	ownerKey, owner := issueKey(t, r, "owner")
	execKey, executor := issueKey(t, r, "executor")

	org := seedOrgWithMember(t, "acme", owner, "admin")
	addOrgMember(t, org, executor, "executor")

	source := seedFeatureSession(t, owner, &org, true)
	cpID := makeCheckpoint(t, r, source, `{"description":"shared work"}`, keyHdr(ownerKey))

	w := rec(r, "POST", "/api/v1/checkpoints/"+cpID.String()+"/fork", `{}`, keyHdr(execKey))
	if w.Code != http.StatusCreated {
		t.Fatalf("executor fork: want 201, got %d: %s", w.Code, w.Body)
	}
	forkID := forkedSessionID(t, w.Body.Bytes())

	var createdBy string
	if err := testDB.Get(&createdBy, `SELECT created_by FROM sessions WHERE id = $1`, forkID); err != nil {
		t.Fatalf("read fork owner: %v", err)
	}
	if createdBy != executor {
		t.Errorf("fork created_by = %q, want the caller %q", createdBy, executor)
	}

	// The source session plus the fork put the executor at 2 of 2; the source
	// belongs to the owner, so only the fork counts. One more fork reaches the
	// cap, the next must be refused.
	w = rec(r, "POST", "/api/v1/checkpoints/"+cpID.String()+"/fork", `{}`, keyHdr(execKey))
	if w.Code != http.StatusCreated {
		t.Fatalf("second fork: want 201, got %d: %s", w.Code, w.Body)
	}
	w = rec(r, "POST", "/api/v1/checkpoints/"+cpID.String()+"/fork", `{}`, keyHdr(execKey))
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("fork past the quota: want 429, got %d: %s — forking escapes the session quota",
			w.Code, w.Body)
	}
}

// TestForkEnforcesResourceCeilings: the overrides on a fork request are a
// session request like any other and must not exceed the deployment's caps.
func TestForkEnforcesResourceCeilings(t *testing.T) {
	t.Setenv("MAX_SESSION_CPU", "2")
	t.Setenv("MAX_SESSION_MEMORY_MB", "1024")

	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	key, principal := issueKey(t, r, "alice")
	source := seedFeatureSession(t, principal, nil, true)
	cpID := makeCheckpoint(t, r, source, `{"description":"baseline"}`, keyHdr(key))

	for _, tc := range []struct {
		name string
		body string
		want int
	}{
		{"cpu above ceiling", `{"cpu_limit":64}`, http.StatusBadRequest},
		{"memory above ceiling", `{"memory_limit_mb":65536}`, http.StatusBadRequest},
		{"zero cpu", `{"cpu_limit":0}`, http.StatusBadRequest},
		{"negative cpu", `{"cpu_limit":-1}`, http.StatusBadRequest},
		{"negative memory", `{"memory_limit_mb":-512}`, http.StatusBadRequest},
		{"name beyond the column", fmt.Sprintf(`{"name":%q}`, strings.Repeat("x", 5000)), http.StatusBadRequest},
		{"within the ceilings", `{"cpu_limit":2,"memory_limit_mb":1024}`, http.StatusCreated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := rec(r, "POST", "/api/v1/checkpoints/"+cpID.String()+"/fork", tc.body, keyHdr(key))
			if w.Code != tc.want {
				t.Errorf("want %d, got %d: %s", tc.want, w.Code, w.Body)
			}
		})
	}

	// Only the accepted request may have produced a session.
	var forks int
	if err := testDB.Get(&forks,
		`SELECT COUNT(*) FROM sessions WHERE forked_from_checkpoint_id = $1`, cpID); err != nil {
		t.Fatalf("count forks: %v", err)
	}
	if forks != 1 {
		t.Errorf("%d forks exist, want 1 — a rejected request still created a session", forks)
	}
}

// TestForkRefusesWithdrawnImage: a fork reruns the source session's image. If
// the deployment has since removed that image from the allowlist — the usual
// reason being a vulnerability — a fork must not bring it back.
func TestForkRefusesWithdrawnImage(t *testing.T) {
	truncateFeatureTables(t)

	key := ""
	principal := ""
	var cpID uuid.UUID

	// Phase 1: no allowlist, so the session and its checkpoint are created.
	r, _, _ := featureRouter(t, testDB)
	key, principal = issueKey(t, r, "alice")
	source := seedFeatureSession(t, principal, nil, true)
	cpID = makeCheckpoint(t, r, source, `{"description":"recorded while allowed"}`, keyHdr(key))

	// Phase 2: the same data, a router whose allowlist no longer contains the
	// session's image.
	r2, _, _ := featureRouter(t, testDB, func(c *config.Config) {
		c.Docker.ImageAllowlist = []string{"alpine:3.20"}
	})

	w := rec(r2, "POST", "/api/v1/checkpoints/"+cpID.String()+"/fork", `{}`, keyHdr(key))
	if w.Code != http.StatusBadRequest {
		t.Errorf("fork of a withdrawn image: want 400, got %d: %s", w.Code, w.Body)
	}

	var forks int
	if err := testDB.Get(&forks,
		`SELECT COUNT(*) FROM sessions WHERE forked_from_checkpoint_id = $1`, cpID); err != nil {
		t.Fatalf("count forks: %v", err)
	}
	if forks != 0 {
		t.Errorf("%d forks were created with a withdrawn image", forks)
	}
}

// TestForkRefusesTamperedCheckpoint: restore verifies the HMAC, and fork has to
// as well — it writes the same recorded bytes into a live workspace.
func TestForkRefusesTamperedCheckpoint(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	source := seedFeatureSession(t, "master", nil, true)
	cpID := makeCheckpoint(t, r, source, `{"description":"authentic"}`, masterHdr())

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

	if _, err := testDB.Exec(
		`UPDATE replay_checkpoints SET command = 'rm -rf /' WHERE id = $1`, cpID); err != nil {
		t.Fatalf("tamper: %v", err)
	}

	w := rec(r, "POST", "/api/v1/checkpoints/"+cpID.String()+"/fork", `{}`, masterHdr())
	if w.Code != http.StatusConflict {
		t.Errorf("fork of a tampered checkpoint: want 409, got %d: %s", w.Code, w.Body)
	}

	var forks int
	if err := testDB.Get(&forks,
		`SELECT COUNT(*) FROM sessions WHERE forked_from_checkpoint_id = $1`, cpID); err != nil {
		t.Fatalf("count forks: %v", err)
	}
	if forks != 0 {
		t.Errorf("%d forks were created from a tampered checkpoint", forks)
	}
}

// TestReplayViewerMayReadButNotAct: within an org, reading history is a
// viewer-level action while restoring and forking change live state.
func TestReplayViewerMayReadButNotAct(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	ownerKey, owner := issueKey(t, r, "owner")
	viewerKey, viewer := issueKey(t, r, "viewer")

	org := seedOrgWithMember(t, "acme", owner, "admin")
	addOrgMember(t, org, viewer, "viewer")

	session := seedFeatureSession(t, owner, &org, true)
	cpID := makeCheckpoint(t, r, session, `{"description":"owner's work"}`, keyHdr(ownerKey))

	base := "/api/v1/sessions/" + session.String() + "/checkpoints/" + cpID.String()

	for _, tc := range []struct {
		name   string
		method string
		path   string
		want   int
	}{
		{"list", "GET", "/api/v1/sessions/" + session.String() + "/checkpoints", http.StatusOK},
		{"get", "GET", base, http.StatusOK},
		{"restore", "POST", base + "/restore", http.StatusForbidden},
		{"fork", "POST", "/api/v1/checkpoints/" + cpID.String() + "/fork", http.StatusForbidden},
		{"delete", "DELETE", base, http.StatusForbidden},
	} {
		t.Run("viewer "+tc.name, func(t *testing.T) {
			w := rec(r, tc.method, tc.path, "{}", keyHdr(viewerKey))
			if w.Code != tc.want {
				t.Errorf("want %d, got %d: %s", tc.want, w.Code, w.Body)
			}
		})
	}

	// The checkpoint must have survived the refused delete.
	if w := rec(r, "GET", base, "", keyHdr(ownerKey)); w.Code != http.StatusOK {
		t.Errorf("checkpoint is gone after a refused delete: got %d", w.Code)
	}
}

// TestCheckpointListPagingIsClamped: limit and offset reach a LIMIT/OFFSET
// clause. In Postgres a negative LIMIT means "no limit" and a negative OFFSET
// is a query error, so both have to be clamped rather than passed through.
func TestCheckpointListPagingIsClamped(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	session := seedFeatureSession(t, "master", nil, true)
	for i := 0; i < 3; i++ {
		makeCheckpoint(t, r, session, fmt.Sprintf(`{"description":"step %d"}`, i), masterHdr())
	}

	type page struct {
		Checkpoints []map[string]any `json:"checkpoints"`
		Limit       int              `json:"limit"`
		Offset      int              `json:"offset"`
	}

	for _, tc := range []struct {
		name       string
		query      string
		wantLen    int
		wantLimit  int
		wantOffset int
	}{
		{"no paging", "", 3, 50, 0},
		{"limit 2", "?limit=2", 2, 2, 0},
		{"offset 1", "?offset=1", 2, 50, 1},
		{"negative limit", "?limit=-1", 3, 50, 0},
		{"zero limit", "?limit=0", 3, 50, 0},
		{"limit past the cap", "?limit=100000", 3, 100, 0},
		{"negative offset", "?offset=-5", 3, 50, 0},
		{"non-numeric limit", "?limit=abc", 3, 50, 0},
		{"non-numeric offset", "?offset=%27%3B--", 3, 50, 0},
		{"offset past the end", "?offset=99", 0, 50, 99},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := rec(r, "GET", "/api/v1/sessions/"+session.String()+"/checkpoints"+tc.query, "", masterHdr())
			if w.Code != http.StatusOK {
				t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
			}
			var got page
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode page: %v", err)
			}
			if len(got.Checkpoints) != tc.wantLen {
				t.Errorf("returned %d checkpoints, want %d", len(got.Checkpoints), tc.wantLen)
			}
			if got.Limit != tc.wantLimit {
				t.Errorf("echoed limit = %d, want %d", got.Limit, tc.wantLimit)
			}
			if got.Offset != tc.wantOffset {
				t.Errorf("echoed offset = %d, want %d", got.Offset, tc.wantOffset)
			}
		})
	}
}

// TestCheckpointPreviewsStayValidUTF8: previews are cut to 500 bytes. Cutting
// mid-rune produces a byte sequence Postgres rejects for a text column, so a
// checkpoint whose output happened to be non-ASCII failed to record at all.
func TestCheckpointPreviewsStayValidUTF8(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	session := seedFeatureSession(t, "master", nil, true)

	for _, tc := range []struct {
		name string
		text string
	}{
		{"three-byte runes", strings.Repeat("あ", 400)},
		{"four-byte runes", strings.Repeat("𝔘", 400)},
		{"two-byte runes", strings.Repeat("é", 400)},
		{"mixed", strings.Repeat("aé漢𝔘", 200)},
		{"emoji", strings.Repeat("🔒", 400)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]string{
				"description": "utf8 " + tc.name,
				"stdout":      tc.text,
				"stderr":      tc.text,
			})
			if err != nil {
				t.Fatalf("encode body: %v", err)
			}

			cpID := makeCheckpoint(t, r, session, string(body), masterHdr())

			var stdout, stderr string
			err = testDB.QueryRow(
				`SELECT stdout_preview, stderr_preview FROM replay_checkpoints WHERE id = $1`,
				cpID).Scan(&stdout, &stderr)
			if err != nil {
				t.Fatalf("read previews: %v", err)
			}
			for label, preview := range map[string]string{"stdout": stdout, "stderr": stderr} {
				if !utf8.ValidString(preview) {
					t.Errorf("%s preview is not valid UTF-8", label)
				}
				if len(preview) > 500 {
					t.Errorf("%s preview is %d bytes, want at most 500", label, len(preview))
				}
				if !strings.HasPrefix(tc.text, preview) {
					t.Errorf("%s preview is not a prefix of the input", label)
				}
			}
		})
	}
}

// TestCheckpointCapPrunesOldest: the per-session cap is enforced by pruning the
// oldest checkpoint, not by refusing new ones — an agent must never be blocked
// from recording its current state. The pruned snapshot has to be deleted too,
// or the cap bounds rows while the disk keeps growing.
func TestCheckpointCapPrunesOldest(t *testing.T) {
	truncateFeatureTables(t)
	r, stubWS, _ := featureRouter(t, testDB)

	session := seedFeatureSession(t, "master", nil, true)

	firstID := makeCheckpoint(t, r, session, `{"description":"oldest"}`, masterHdr())
	for i := 1; i < replay.MaxCheckpointsPerSession; i++ {
		makeCheckpoint(t, r, session, fmt.Sprintf(`{"description":"step %d"}`, i), masterHdr())
	}

	var count int
	if err := testDB.Get(&count,
		`SELECT COUNT(*) FROM replay_checkpoints WHERE session_id = $1`, session); err != nil {
		t.Fatalf("count checkpoints: %v", err)
	}
	if count != replay.MaxCheckpointsPerSession {
		t.Fatalf("%d checkpoints recorded, want %d", count, replay.MaxCheckpointsPerSession)
	}

	deletedBefore := len(stubWS.deleted)

	// One past the cap.
	makeCheckpoint(t, r, session, `{"description":"one too many"}`, masterHdr())

	if err := testDB.Get(&count,
		`SELECT COUNT(*) FROM replay_checkpoints WHERE session_id = $1`, session); err != nil {
		t.Fatalf("count checkpoints: %v", err)
	}
	if count != replay.MaxCheckpointsPerSession {
		t.Errorf("%d checkpoints after exceeding the cap, want %d", count, replay.MaxCheckpointsPerSession)
	}

	var stillThere int
	if err := testDB.Get(&stillThere,
		`SELECT COUNT(*) FROM replay_checkpoints WHERE id = $1`, firstID); err != nil {
		t.Fatalf("look for the oldest checkpoint: %v", err)
	}
	if stillThere != 0 {
		t.Error("the oldest checkpoint survived; something newer was pruned instead")
	}
	if len(stubWS.deleted) != deletedBefore+1 {
		t.Errorf("snapshot deletions = %d, want %d — the pruned archive was left on disk",
			len(stubWS.deleted), deletedBefore+1)
	}
}

// TestCheckpointRedactsSecretEnvVars: a checkpoint captures the environment, so
// it must not become a place where secrets are stored in the clear.
func TestCheckpointRedactsSecretEnvVars(t *testing.T) {
	truncateFeatureTables(t)
	r, _, _ := featureRouter(t, testDB)

	session := seedFeatureSession(t, "master", nil, true)

	secrets := map[string]string{
		"AWS_SECRET_ACCESS_KEY": "AKIAIOSFODNN7EXAMPLE",
		"GITHUB_TOKEN":          "ghp_realtokenvalue",
		"DB_PASSWORD":           "hunter2",
		"MY_API_KEY":            "sk-livekey",
		"OPENAI_API_SECRET":     "sk-anotherone",
		"PATH":                  "/usr/bin",
		"LANG":                  "C.UTF-8",
	}
	body, err := json.Marshal(map[string]any{"description": "env capture", "env_vars": secrets})
	if err != nil {
		t.Fatalf("encode body: %v", err)
	}

	cpID := makeCheckpoint(t, r, session, string(body), masterHdr())

	var raw []byte
	if err := testDB.Get(&raw,
		`SELECT env_vars_snapshot FROM replay_checkpoints WHERE id = $1`, cpID); err != nil {
		t.Fatalf("read env snapshot: %v", err)
	}
	stored := string(raw)

	for name, value := range secrets {
		if name == "PATH" || name == "LANG" {
			if !strings.Contains(stored, value) {
				t.Errorf("%s was redacted although it holds no secret", name)
			}
			continue
		}
		if strings.Contains(stored, value) {
			t.Errorf("the value of %s is stored in the clear in the checkpoint", name)
		}
	}

	// The same values must not leak through the API response either.
	w := rec(r, "GET", "/api/v1/sessions/"+session.String()+"/checkpoints/"+cpID.String(), "", masterHdr())
	if w.Code != http.StatusOK {
		t.Fatalf("get checkpoint: want 200, got %d: %s", w.Code, w.Body)
	}
	for name, value := range secrets {
		if name == "PATH" || name == "LANG" {
			continue
		}
		if strings.Contains(w.Body.String(), value) {
			t.Errorf("the API response leaks the value of %s", name)
		}
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