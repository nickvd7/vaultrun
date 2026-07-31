package cost

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func testTracker() *Tracker {
	return &Tracker{signingKey: []byte("test-signing-key-do-not-use-in-production")}
}

// validMetric returns a fully populated metric so each tamper test can mutate
// exactly one field.
func validMetric() *CostMetric {
	rateID := uuid.New()

	return &CostMetric{
		ID:              uuid.New(),
		SessionID:       uuid.New(),
		PeriodStart:     time.Unix(1753900000, 0).UTC(),
		PeriodEnd:       time.Unix(1753903600, 0).UTC(),
		CPUCoreHours:    1.5,
		MemoryGBHours:   4.0,
		GPUHours:        0,
		WorkspaceGBDays: 0.25,
		SnapshotGBDays:  0.1,
		ArtifactGBDays:  0.05,
		EgressGB:        2.0,
		IngressGB:       10.0,
		ComputeCost:     0.062,
		StorageCost:     0.0031,
		NetworkCost:     0.18,
		TotalCost:       0.2451,
		RateID:          &rateID,
		CreatedAt:       time.Unix(1753903600, 0).UTC(),
	}
}

func signed(t *Tracker, m *CostMetric) *CostMetric {
	m.Checksum = t.checksum(m)
	m.Signature = t.sign(m)
	return m
}

func TestVerifyMetricAcceptsUntampered(t *testing.T) {
	tr := testTracker()
	m := signed(tr, validMetric())

	if !tr.VerifyMetric(m) {
		t.Error("VerifyMetric() = false for an untampered metric, want true")
	}
}

// TestChecksumCoversUsageCounters is the regression test for the original
// scheme, which hashed only SessionID, the period and three of the four cost
// figures. The raw usage counters and the rate card were unprotected, so the
// evidence behind a charge could be rewritten while the digest still matched.
func TestChecksumCoversUsageCounters(t *testing.T) {
	otherRate := uuid.New()

	tampers := []struct {
		field string
		apply func(*CostMetric)
	}{
		{"SessionID", func(m *CostMetric) { m.SessionID = uuid.New() }},
		{"PeriodStart", func(m *CostMetric) { m.PeriodStart = m.PeriodStart.Add(time.Hour) }},
		{"PeriodEnd", func(m *CostMetric) { m.PeriodEnd = m.PeriodEnd.Add(time.Hour) }},

		// Raw usage counters — unprotected in the original scheme.
		{"CPUCoreHours", func(m *CostMetric) { m.CPUCoreHours = 999 }},
		{"MemoryGBHours", func(m *CostMetric) { m.MemoryGBHours = 999 }},
		{"GPUHours", func(m *CostMetric) { m.GPUHours = 999 }},
		{"WorkspaceGBDays", func(m *CostMetric) { m.WorkspaceGBDays = 999 }},
		{"SnapshotGBDays", func(m *CostMetric) { m.SnapshotGBDays = 999 }},
		{"ArtifactGBDays", func(m *CostMetric) { m.ArtifactGBDays = 999 }},
		{"EgressGB", func(m *CostMetric) { m.EgressGB = 999 }},
		{"IngressGB", func(m *CostMetric) { m.IngressGB = 999 }},

		// Derived costs.
		{"ComputeCost", func(m *CostMetric) { m.ComputeCost = 0 }},
		{"StorageCost", func(m *CostMetric) { m.StorageCost = 0 }},
		{"NetworkCost", func(m *CostMetric) { m.NetworkCost = 0 }},
		{"TotalCost", func(m *CostMetric) { m.TotalCost = 0 }},

		// Rate card attribution.
		{"RateID", func(m *CostMetric) { m.RateID = &otherRate }},
		{"RateID cleared", func(m *CostMetric) { m.RateID = nil }},
	}

	for _, tc := range tampers {
		t.Run(tc.field, func(t *testing.T) {
			tr := testTracker()
			m := signed(tr, validMetric())

			tc.apply(m)

			if tr.VerifyMetric(m) {
				t.Errorf("VerifyMetric() = true after tampering with %s — checksum does not cover this field", tc.field)
			}
		})
	}
}

// TestVerifyMetricRejectsForgedSignature covers supplying a signature rather
// than editing a field. An attacker who can write to the database can also
// recompute the checksum, so only the HMAC stops them.
func TestVerifyMetricRejectsForgedSignature(t *testing.T) {
	tr := testTracker()

	forged := []struct {
		name string
		sig  string
	}{
		{"empty", ""},
		{"all zeroes", strings.Repeat("0", 64)},
		{"not hex", strings.Repeat("z", 64)},
		{"truncated", "abcd"},
		{"odd length", "abc"},
	}

	for _, tc := range forged {
		t.Run(tc.name, func(t *testing.T) {
			m := validMetric()
			m.Checksum = tr.checksum(m)
			m.Signature = tc.sig

			if tr.VerifyMetric(m) {
				t.Errorf("VerifyMetric() = true for forged signature %q, want false", tc.sig)
			}
		})
	}
}

// TestVerifyMetricRejectsRecomputedChecksum: an attacker who lowers TotalCost
// and recomputes the checksum still cannot produce a matching HMAC without
// the signing key.
func TestVerifyMetricRejectsRecomputedChecksum(t *testing.T) {
	tr := testTracker()
	m := signed(tr, validMetric())

	m.TotalCost = 0.0001
	m.Checksum = tr.checksum(m) // attacker recomputes the unkeyed digest

	if tr.VerifyMetric(m) {
		t.Error("VerifyMetric() = true after lowering the cost and recomputing the checksum, want false")
	}
}

func TestVerifyMetricRejectsWrongKey(t *testing.T) {
	signer := &Tracker{signingKey: []byte("key-one")}
	verifier := &Tracker{signingKey: []byte("key-two")}

	m := signed(signer, validMetric())

	if verifier.VerifyMetric(m) {
		t.Error("VerifyMetric() = true with a different signing key, want false")
	}
}

// TestVerifyMetricFailsClosedWithoutKey: a deployment that never set
// AUDIT_HMAC_KEY must not treat every metric as authentic.
func TestVerifyMetricFailsClosedWithoutKey(t *testing.T) {
	unkeyed := &Tracker{signingKey: nil}
	m := signed(unkeyed, validMetric())

	if unkeyed.VerifyMetric(m) {
		t.Error("VerifyMetric() = true with no signing key configured, want false (fail closed)")
	}
}

// TestChecksumDistinguishesSmallAmounts is the regression test for %f
// formatting, which truncated at six decimals: 0.0000001 and 0.0000002 both
// rendered as "0.000000" and produced the same digest.
func TestChecksumDistinguishesSmallAmounts(t *testing.T) {
	tr := testTracker()

	a := validMetric()
	a.TotalCost = 0.0000001

	b := validMetric()
	b.SessionID = a.SessionID
	b.PeriodStart = a.PeriodStart
	b.PeriodEnd = a.PeriodEnd
	b.RateID = a.RateID
	b.CPUCoreHours = a.CPUCoreHours
	b.MemoryGBHours = a.MemoryGBHours
	b.GPUHours = a.GPUHours
	b.WorkspaceGBDays = a.WorkspaceGBDays
	b.SnapshotGBDays = a.SnapshotGBDays
	b.ArtifactGBDays = a.ArtifactGBDays
	b.EgressGB = a.EgressGB
	b.IngressGB = a.IngressGB
	b.ComputeCost = a.ComputeCost
	b.StorageCost = a.StorageCost
	b.NetworkCost = a.NetworkCost
	b.TotalCost = 0.0000002

	if tr.checksum(a) == tr.checksum(b) {
		t.Error("checksum() collided for 0.0000001 and 0.0000002 — float formatting loses precision")
	}
}

// TestChecksumResistsBoundaryConfusion: without a field separator, shifting a
// character between two adjacent string fields would not change the digest.
func TestChecksumResistsBoundaryConfusion(t *testing.T) {
	tr := testTracker()

	// Two metrics whose period timestamps differ by one second in opposite
	// directions produce the same total elapsed time but must not collide.
	a := validMetric()
	a.PeriodStart = time.Unix(1753900000, 0).UTC()
	a.PeriodEnd = time.Unix(1753903601, 0).UTC()

	b := validMetric()
	b.SessionID = a.SessionID
	b.RateID = a.RateID
	b.PeriodStart = time.Unix(1753900001, 0).UTC()
	b.PeriodEnd = time.Unix(1753903602, 0).UTC()

	if tr.checksum(a) == tr.checksum(b) {
		t.Error("checksum() collided for different period boundaries")
	}
}

// TestChecksumIsStable guards against a digest that changes between calls,
// which would make every stored metric fail verification after a restart.
func TestChecksumIsStable(t *testing.T) {
	tr := testTracker()
	m := validMetric()

	first := tr.checksum(m)
	for i := 0; i < 5; i++ {
		if got := tr.checksum(m); got != first {
			t.Fatalf("checksum() is not deterministic: call %d gave %s, first gave %s", i+2, got, first)
		}
	}
}

// TestChecksumNormalisesTimezone: a metric read back from Postgres may carry a
// different location than the one that was written. The digest must not depend
// on it, or every metric would fail verification depending on server timezone.
func TestChecksumNormalisesTimezone(t *testing.T) {
	tr := testTracker()

	utc := validMetric()

	shifted := validMetric()
	shifted.SessionID = utc.SessionID
	shifted.RateID = utc.RateID
	// Same instant, different location.
	shifted.PeriodStart = utc.PeriodStart.In(time.FixedZone("CEST", 2*60*60))
	shifted.PeriodEnd = utc.PeriodEnd.In(time.FixedZone("CEST", 2*60*60))

	if tr.checksum(utc) != tr.checksum(shifted) {
		t.Error("checksum() differs for the same instant in another timezone — metrics would fail verification after a DB round-trip")
	}
}

// TestSignReturnsEmptyWithoutKey documents that an unkeyed tracker produces no
// signature rather than an HMAC over an empty key, which would be forgeable.
func TestSignReturnsEmptyWithoutKey(t *testing.T) {
	unkeyed := &Tracker{signingKey: nil}
	m := validMetric()
	m.Checksum = unkeyed.checksum(m)

	if sig := unkeyed.sign(m); sig != "" {
		t.Errorf("sign() = %q with no key, want empty string", sig)
	}
}
