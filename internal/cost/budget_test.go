package cost

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

// TestBudgetValidationRejectsUnusableLimits covers the values that would make a
// budget meaningless rather than restrictive.
//
// A stored negative or zero limit reads as "already over budget", which either
// silences the derived alert or fires it on every metric depending on the
// comparison — both are worse than refusing the input.
func TestBudgetValidationRejectsUnusableLimits(t *testing.T) {
	month := time.Now().Format("2006-01")

	cases := []struct {
		name string
		opts BudgetOpts
	}{
		{"negative limit", BudgetOpts{MonthlyLimit: -100, AlertThreshold: 0.8, Month: month}},
		{"zero limit", BudgetOpts{MonthlyLimit: 0, AlertThreshold: 0.8, Month: month}},
		{"NaN limit", BudgetOpts{MonthlyLimit: math.NaN(), AlertThreshold: 0.8, Month: month}},
		{"positive infinite limit", BudgetOpts{MonthlyLimit: math.Inf(1), AlertThreshold: 0.8, Month: month}},
		{"negative infinite limit", BudgetOpts{MonthlyLimit: math.Inf(-1), AlertThreshold: 0.8, Month: month}},
		{"limit beyond column precision", BudgetOpts{MonthlyLimit: MaxMonthlyLimit + 1, AlertThreshold: 0.8, Month: month}},
		{"zero threshold", BudgetOpts{MonthlyLimit: 100, AlertThreshold: 0, Month: month}},
		{"negative threshold", BudgetOpts{MonthlyLimit: 100, AlertThreshold: -0.5, Month: month}},
		{"threshold above one", BudgetOpts{MonthlyLimit: 100, AlertThreshold: 1.5, Month: month}},
		{"NaN threshold", BudgetOpts{MonthlyLimit: 100, AlertThreshold: math.NaN(), Month: month}},
		{"empty month", BudgetOpts{MonthlyLimit: 100, AlertThreshold: 0.8, Month: ""}},
		{"month with day", BudgetOpts{MonthlyLimit: 100, AlertThreshold: 0.8, Month: "2026-07-01"}},
		{"month out of range", BudgetOpts{MonthlyLimit: 100, AlertThreshold: 0.8, Month: "2026-13"}},
		{"month as sql fragment", BudgetOpts{MonthlyLimit: 100, AlertThreshold: 0.8, Month: "2026-07'; DROP TABLE cost_budgets--"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.opts.validate()
			if err == nil {
				t.Fatalf("validate() accepted %+v", tc.opts)
			}
			if !errors.Is(err, ErrInvalidBudget) {
				t.Errorf("error %v does not wrap ErrInvalidBudget, so the handler cannot answer 400", err)
			}
		})
	}
}

// TestBudgetValidationAcceptsRealisticInput guards against over-tightening: the
// boundary values a deployment would actually use must pass.
func TestBudgetValidationAcceptsRealisticInput(t *testing.T) {
	cases := []BudgetOpts{
		{MonthlyLimit: 0.01, AlertThreshold: 0.01, Month: "2026-01"},
		{MonthlyLimit: 500, AlertThreshold: 0.8, Month: "2026-07"},
		{MonthlyLimit: MaxMonthlyLimit, AlertThreshold: 1, Month: "2026-12"},
	}

	for _, opts := range cases {
		if err := opts.validate(); err != nil {
			t.Errorf("validate() rejected legitimate budget %+v: %v", opts, err)
		}
	}
}

// TestScopePredicatesRestrictNonMaster asserts the shape of the generated SQL:
// the master key gets no predicate, everyone else gets one bound to their
// principal. The queries themselves are exercised end to end in the handler
// integration tests; this pins the invariant that a non-master scope never
// produces an empty filter.
func TestScopePredicatesRestrictNonMaster(t *testing.T) {
	if frag, args := DeploymentScope().sessionPredicate("s", 2); frag != "" || args != nil {
		t.Errorf("master session predicate = %q with args %v, want unrestricted", frag, args)
	}
	if frag, args := DeploymentScope().alertPredicate(2); frag != "" || args != nil {
		t.Errorf("master alert predicate = %q with args %v, want unrestricted", frag, args)
	}

	frag, args := ActorScope("key-uuid").sessionPredicate("s", 2)
	if frag == "" {
		t.Fatal("actor session predicate is empty — every tenant would see every session")
	}
	if !strings.Contains(frag, "s.created_by = $2") || !strings.Contains(frag, "org_members") {
		t.Errorf("session predicate %q does not cover both ownership and org membership", frag)
	}
	if len(args) != 1 || args[0] != "key-uuid" {
		t.Errorf("session predicate args = %v, want the actor once", args)
	}

	frag, args = ActorScope("key-uuid").alertPredicate(4)
	if frag == "" {
		t.Fatal("actor alert predicate is empty — every tenant would see every alert")
	}
	if !strings.Contains(frag, "$4") {
		t.Errorf("alert predicate %q ignores the requested placeholder index", frag)
	}
	if len(args) != 1 || args[0] != "key-uuid" {
		t.Errorf("alert predicate args = %v, want the actor once", args)
	}
}
