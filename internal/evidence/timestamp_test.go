package evidence_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/digitorus/pkcs7"

	"github.com/nickvd7/vaultrun/internal/evidence"
	"github.com/nickvd7/vaultrun/internal/evidence/evidencetest"
)

func signedEnvelope(t *testing.T) (*evidence.Signer, *evidence.Envelope) {
	t.Helper()
	seed, _, err := evidence.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	s, err := evidence.ParseSigningKey(seed)
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.Sign(&evidence.Pack{Schema: evidence.PackSchema, ID: "p", Session: evidence.Session{ID: "s"}})
	if err != nil {
		t.Fatal(err)
	}
	return s, env
}

func TestTimestampRoundTrip(t *testing.T) {
	tsa := evidencetest.NewTSA(t, evidencetest.Options{})
	s, env := signedEnvelope(t)
	before := time.Now().Add(-2 * time.Second)

	if err := evidence.RequestTimestamp(context.Background(), http.DefaultClient, tsa.URL, env); err != nil {
		t.Fatalf("RequestTimestamp: %v", err)
	}
	info, err := evidence.VerifyTimestamp(env, tsa.Roots)
	if err != nil {
		t.Fatalf("VerifyTimestamp: %v", err)
	}
	if info.Time.Before(before) || info.Time.After(time.Now().Add(2*time.Second)) || info.TSA == "" || info.Serial == "" {
		t.Errorf("info = %+v", info)
	}

	// The token travels in the file: reopen and check both signatures.
	raw, _ := json.Marshal(env)
	reopened, err := evidence.Open(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Verify(s.PublicKey()); err != nil {
		t.Fatalf("pack signature after stamping: %v", err)
	}
	if _, err := evidence.VerifyTimestamp(reopened, tsa.Roots); err != nil {
		t.Fatalf("timestamp after reopening: %v", err)
	}
}

func TestTimestampUntrustedRoot(t *testing.T) {
	tsa := evidencetest.NewTSA(t, evidencetest.Options{})
	other := evidencetest.NewTSA(t, evidencetest.Options{})
	_, env := signedEnvelope(t)
	if err := evidence.RequestTimestamp(context.Background(), http.DefaultClient, tsa.URL, env); err != nil {
		t.Fatal(err)
	}
	info, err := evidence.VerifyTimestamp(env, other.Roots)
	if !errors.Is(err, evidence.ErrTimestampUntrusted) || info == nil || info.Time.IsZero() {
		t.Errorf("other root: want ErrTimestampUntrusted with info, got %v, %+v", err, info)
	}
	// Not in the system roots either.
	if _, err := evidence.VerifyTimestamp(env, nil); !errors.Is(err, evidence.ErrTimestampUntrusted) {
		t.Errorf("system roots: want ErrTimestampUntrusted, got %v", err)
	}
}

func TestTimestampRequiresTimeStampingUsage(t *testing.T) {
	tsa := evidencetest.NewTSA(t, evidencetest.Options{NoTimeStampingEKU: true})
	_, env := signedEnvelope(t)
	if err := evidence.RequestTimestamp(context.Background(), http.DefaultClient, tsa.URL, env); err != nil {
		t.Fatal(err)
	}
	if _, err := evidence.VerifyTimestamp(env, tsa.Roots); !errors.Is(err, evidence.ErrTimestampUntrusted) {
		t.Errorf("certificate without time-stamping usage accepted: %v", err)
	}
}

func TestTimestampDetectsTampering(t *testing.T) {
	tsa := evidencetest.NewTSA(t, evidencetest.Options{})
	_, env := signedEnvelope(t)
	if err := evidence.RequestTimestamp(context.Background(), http.DefaultClient, tsa.URL, env); err != nil {
		t.Fatal(err)
	}

	// A token moved onto another pack's signature does not fit.
	_, other := signedEnvelope(t)
	other.Timestamp = env.Timestamp
	if _, err := evidence.VerifyTimestamp(other, tsa.Roots); !errors.Is(err, evidence.ErrTimestampMismatch) {
		t.Errorf("moved token: want ErrTimestampMismatch, got %v", err)
	}

	// Every byte of what the TSA vouches for (the TSTInfo: imprint, time,
	// serial, policy) and of the TSA's signature is protected. (CMS also has
	// unsigned bytes, such as the digestAlgorithms hint in SignedData; changing
	// those does not change what the token says, so they are not tested.)
	p7, err := pkcs7.Parse(env.Timestamp)
	if err != nil {
		t.Fatal(err)
	}
	start := bytes.Index(env.Timestamp, p7.Content)
	if start < 0 {
		t.Fatal("TSTInfo not found in token")
	}
	protected := []int{}
	for i := start; i < start+len(p7.Content); i++ {
		protected = append(protected, i)
	}
	for i := len(env.Timestamp) - 64; i < len(env.Timestamp); i++ { // the ECDSA signature ends the token
		protected = append(protected, i)
	}
	for _, i := range protected {
		changed := *env
		changed.Timestamp = append([]byte(nil), env.Timestamp...)
		changed.Timestamp[i] ^= 0x01
		if _, err := evidence.VerifyTimestamp(&changed, tsa.Roots); err == nil {
			t.Fatalf("protected byte %d of the token flipped: still verifies", i)
		}
	}

	none := *env
	none.Timestamp = nil
	if _, err := evidence.VerifyTimestamp(&none, tsa.Roots); err == nil {
		t.Error("missing timestamp verified")
	}
}

func TestRequestTimestampFailures(t *testing.T) {
	ctx := context.Background()
	_, env := signedEnvelope(t)

	down := evidencetest.NewTSA(t, evidencetest.Options{})
	down.Fail = true
	if err := evidence.RequestTimestamp(ctx, http.DefaultClient, down.URL, env); err == nil || env.Timestamp != nil {
		t.Errorf("failing TSA: err=%v timestamp=%d bytes", err, len(env.Timestamp))
	}

	replay := evidencetest.NewTSA(t, evidencetest.Options{})
	replay.IgnoreNonce = true
	if err := evidence.RequestTimestamp(ctx, http.DefaultClient, replay.URL, env); err == nil {
		t.Error("response without our nonce accepted")
	}

	if err := evidence.RequestTimestamp(ctx, http.DefaultClient, "http://127.0.0.1:1", env); err == nil {
		t.Error("unreachable TSA accepted")
	}
	unsigned := &evidence.Envelope{}
	if err := evidence.RequestTimestamp(ctx, http.DefaultClient, down.URL, unsigned); err == nil {
		t.Error("unsigned envelope stamped")
	}
}

// TestRealTSA checks interoperability with a public TSA whose root is in the
// system pool, e.g. VAULTRUN_TEST_TSA_URL=http://timestamp.digicert.com.
func TestRealTSA(t *testing.T) {
	url := os.Getenv("VAULTRUN_TEST_TSA_URL")
	if url == "" {
		t.Skip("VAULTRUN_TEST_TSA_URL not set")
	}
	_, env := signedEnvelope(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := evidence.RequestTimestamp(ctx, http.DefaultClient, url, env); err != nil {
		t.Fatalf("RequestTimestamp(%s): %v", url, err)
	}
	info, err := evidence.VerifyTimestamp(env, nil)
	if err != nil {
		t.Fatalf("VerifyTimestamp with system roots: %v", err)
	}
	t.Logf("stamped %s by %s (serial %s)", info.Time, info.TSA, info.Serial)
}

func TestLoadCertPool(t *testing.T) {
	tsa := evidencetest.NewTSA(t, evidencetest.Options{})
	if pool, err := evidence.LoadCertPool(tsa.RootFile); err != nil || pool == nil {
		t.Errorf("LoadCertPool: %v", err)
	}
	if pool, err := evidence.LoadCertPool(""); err != nil || pool != nil {
		t.Errorf("empty path: want nil pool (system roots), got %v %v", pool, err)
	}
	bad := t.TempDir() + "/bad.pem"
	_ = os.WriteFile(bad, []byte("not a cert"), 0o644)
	if _, err := evidence.LoadCertPool(bad); err == nil {
		t.Error("garbage accepted")
	}
}
