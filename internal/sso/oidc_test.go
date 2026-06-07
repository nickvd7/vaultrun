package sso

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

// testKeyPair generates an RSA key pair, a JWK public key set containing it,
// and returns a signer function that mints ID tokens for the given claims.
type testKeyPair struct {
	kid     string
	priv    *rsa.PrivateKey
	pubSet  jwk.Set
	privKey jwk.Key
}

func newTestKeyPair(t *testing.T, kid string) *testKeyPair {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pubKey, err := jwk.Import(priv.Public())
	if err != nil {
		t.Fatalf("import public key: %v", err)
	}
	if err := pubKey.Set(jwk.KeyIDKey, kid); err != nil {
		t.Fatalf("set kid: %v", err)
	}
	if err := pubKey.Set(jwk.AlgorithmKey, jwa.RS256()); err != nil {
		t.Fatalf("set alg: %v", err)
	}
	set := jwk.NewSet()
	if err := set.AddKey(pubKey); err != nil {
		t.Fatalf("add key to set: %v", err)
	}

	privKey, err := jwk.Import(priv)
	if err != nil {
		t.Fatalf("import private key: %v", err)
	}
	if err := privKey.Set(jwk.KeyIDKey, kid); err != nil {
		t.Fatalf("set kid: %v", err)
	}
	if err := privKey.Set(jwk.AlgorithmKey, jwa.RS256()); err != nil {
		t.Fatalf("set alg: %v", err)
	}

	return &testKeyPair{kid: kid, priv: priv, pubSet: set, privKey: privKey}
}

type idTokenOpts struct {
	issuer  string
	aud     any
	subject string
	nonce   string
	expiry  time.Duration // relative to now; zero means 1 hour
	extra   map[string]any
}

func (k *testKeyPair) sign(t *testing.T, opts idTokenOpts) string {
	t.Helper()
	if opts.expiry == 0 {
		opts.expiry = time.Hour
	}
	b := jwt.NewBuilder().
		Issuer(opts.issuer).
		Subject(opts.subject).
		IssuedAt(time.Now()).
		Expiration(time.Now().Add(opts.expiry))
	if opts.aud != nil {
		switch a := opts.aud.(type) {
		case string:
			b = b.Audience([]string{a})
		case []string:
			b = b.Audience(a)
		}
	}
	if opts.nonce != "" {
		b = b.Claim("nonce", opts.nonce)
	}
	for k, v := range opts.extra {
		b = b.Claim(k, v)
	}
	tok, err := b.Build()
	if err != nil {
		t.Fatalf("build token: %v", err)
	}
	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256(), k.privKey))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return string(signed)
}

// jwksTestServer serves the given key set as JSON and counts requests.
func jwksTestServer(t *testing.T, set jwk.Set) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(set)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestGenerateState(t *testing.T) {
	a, err := GenerateState()
	if err != nil {
		t.Fatalf("GenerateState: %v", err)
	}
	b, err := GenerateState()
	if err != nil {
		t.Fatalf("GenerateState: %v", err)
	}
	if a == b {
		t.Fatal("expected unique state values")
	}
	if len(a) < 32 {
		t.Fatalf("state too short: %d chars", len(a))
	}
}

func TestGenerateNonce(t *testing.T) {
	a, err := GenerateNonce()
	if err != nil {
		t.Fatalf("GenerateNonce: %v", err)
	}
	b, err := GenerateNonce()
	if err != nil {
		t.Fatalf("GenerateNonce: %v", err)
	}
	if a == b {
		t.Fatal("expected unique nonce values")
	}
	if len(a) < 32 {
		t.Fatalf("nonce too short: %d chars", len(a))
	}
}

func TestGenerateCodeVerifier(t *testing.T) {
	a, err := GenerateCodeVerifier()
	if err != nil {
		t.Fatalf("GenerateCodeVerifier: %v", err)
	}
	b, err := GenerateCodeVerifier()
	if err != nil {
		t.Fatalf("GenerateCodeVerifier: %v", err)
	}
	if a == b {
		t.Fatal("expected unique verifier values")
	}
}

func TestAudienceContains(t *testing.T) {
	tests := []struct {
		name string
		aud  any
		want bool
	}{
		{"matching string", "client-123", true},
		{"non-matching string", "other-client", false},
		{"matching []string", []string{"a", "client-123", "b"}, true},
		{"non-matching []string", []string{"a", "b"}, false},
		{"matching []any", []any{"a", "client-123"}, true},
		{"non-matching []any", []any{"a", "b"}, false},
		{"nil", nil, false},
		{"unsupported type", 42, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := audienceContains(tc.aud, "client-123"); got != tc.want {
				t.Errorf("audienceContains(%v) = %v, want %v", tc.aud, got, tc.want)
			}
		})
	}
}

func TestClaimString(t *testing.T) {
	if got := claimString("hello"); got != "hello" {
		t.Errorf("claimString(string) = %q, want %q", got, "hello")
	}
	if got := claimString(42); got != "" {
		t.Errorf("claimString(int) = %q, want empty", got)
	}
	if got := claimString(nil); got != "" {
		t.Errorf("claimString(nil) = %q, want empty", got)
	}
}

func TestAuthCodeURL(t *testing.T) {
	p := &OIDCProvider{
		authURL:     "https://idp.example.com/authorize",
		clientID:    "client-123",
		redirectURL: "https://app.example.com/callback",
		scopes:      []string{"openid", "email"},
	}
	verifier := "test-verifier-value"
	got := p.AuthCodeURL("state-abc", verifier, "nonce-xyz")

	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	q := u.Query()

	if q.Get("response_type") != "code" {
		t.Errorf("response_type = %q, want %q", q.Get("response_type"), "code")
	}
	if q.Get("client_id") != "client-123" {
		t.Errorf("client_id = %q", q.Get("client_id"))
	}
	if q.Get("redirect_uri") != "https://app.example.com/callback" {
		t.Errorf("redirect_uri = %q", q.Get("redirect_uri"))
	}
	if q.Get("state") != "state-abc" {
		t.Errorf("state = %q", q.Get("state"))
	}
	if q.Get("nonce") != "nonce-xyz" {
		t.Errorf("nonce = %q", q.Get("nonce"))
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method = %q", q.Get("code_challenge_method"))
	}
	wantChallenge := pkceChallenge(verifier)
	if q.Get("code_challenge") != wantChallenge {
		t.Errorf("code_challenge = %q, want %q", q.Get("code_challenge"), wantChallenge)
	}
	if !strings.HasPrefix(got, p.authURL+"?") {
		t.Errorf("URL %q does not start with authURL", got)
	}
}

func TestVerifyIDToken(t *testing.T) {
	const issuer = "https://idp.example.com"
	const clientID = "client-123"

	kp := newTestKeyPair(t, "kid-1")
	srv, hits := jwksTestServer(t, kp.pubSet)

	newProvider := func() *OIDCProvider {
		return &OIDCProvider{
			issuerURL: issuer,
			clientID:  clientID,
			jwksURL:   srv.URL,
		}
	}

	t.Run("valid token", func(t *testing.T) {
		p := newProvider()
		raw := kp.sign(t, idTokenOpts{
			issuer:  issuer,
			aud:     clientID,
			subject: "user-1",
			nonce:   "nonce-abc",
			extra:   map[string]any{"email": "user@example.com", "name": "Test User"},
		})
		claims, err := p.verifyIDToken(t.Context(), raw, "nonce-abc")
		if err != nil {
			t.Fatalf("verifyIDToken: %v", err)
		}
		if claims.Sub != "user-1" {
			t.Errorf("Sub = %q, want %q", claims.Sub, "user-1")
		}
		if claims.Email != "user@example.com" {
			t.Errorf("Email = %q", claims.Email)
		}
		if claims.Name != "Test User" {
			t.Errorf("Name = %q", claims.Name)
		}
	})

	t.Run("wrong issuer rejected", func(t *testing.T) {
		p := newProvider()
		raw := kp.sign(t, idTokenOpts{issuer: "https://evil.example.com", aud: clientID, subject: "user-1", nonce: "n"})
		if _, err := p.verifyIDToken(t.Context(), raw, "n"); err == nil {
			t.Fatal("expected error for mismatched issuer")
		}
	})

	t.Run("wrong audience rejected", func(t *testing.T) {
		p := newProvider()
		raw := kp.sign(t, idTokenOpts{issuer: issuer, aud: "other-client", subject: "user-1", nonce: "n"})
		if _, err := p.verifyIDToken(t.Context(), raw, "n"); err == nil {
			t.Fatal("expected error for mismatched audience")
		}
	})

	t.Run("audience as array still matches", func(t *testing.T) {
		p := newProvider()
		raw := kp.sign(t, idTokenOpts{issuer: issuer, aud: []string{"other", clientID}, subject: "user-1", nonce: "n"})
		if _, err := p.verifyIDToken(t.Context(), raw, "n"); err != nil {
			t.Fatalf("expected success with matching aud in array: %v", err)
		}
	})

	t.Run("nonce mismatch rejected", func(t *testing.T) {
		p := newProvider()
		raw := kp.sign(t, idTokenOpts{issuer: issuer, aud: clientID, subject: "user-1", nonce: "expected-nonce"})
		if _, err := p.verifyIDToken(t.Context(), raw, "different-nonce"); err == nil {
			t.Fatal("expected error for nonce mismatch")
		}
	})

	t.Run("missing nonce in token rejected when nonce expected", func(t *testing.T) {
		p := newProvider()
		raw := kp.sign(t, idTokenOpts{issuer: issuer, aud: clientID, subject: "user-1"})
		if _, err := p.verifyIDToken(t.Context(), raw, "expected-nonce"); err == nil {
			t.Fatal("expected error when token has no nonce but one was expected")
		}
	})

	t.Run("missing sub rejected", func(t *testing.T) {
		p := newProvider()
		raw := kp.sign(t, idTokenOpts{issuer: issuer, aud: clientID, nonce: "n"})
		if _, err := p.verifyIDToken(t.Context(), raw, "n"); err == nil {
			t.Fatal("expected error for missing sub claim")
		}
	})

	t.Run("expired token rejected", func(t *testing.T) {
		p := newProvider()
		raw := kp.sign(t, idTokenOpts{issuer: issuer, aud: clientID, subject: "user-1", nonce: "n", expiry: -time.Hour})
		if _, err := p.verifyIDToken(t.Context(), raw, "n"); err == nil {
			t.Fatal("expected error for expired token")
		}
	})

	t.Run("signature from unknown key rejected", func(t *testing.T) {
		p := newProvider()
		other := newTestKeyPair(t, "kid-2")
		raw := other.sign(t, idTokenOpts{issuer: issuer, aud: clientID, subject: "user-1", nonce: "n"})
		if _, err := p.verifyIDToken(t.Context(), raw, "n"); err == nil {
			t.Fatal("expected error for token signed by unknown key")
		}
	})

	t.Run("no jwks url configured", func(t *testing.T) {
		p := &OIDCProvider{issuerURL: issuer, clientID: clientID}
		if _, err := p.verifyIDToken(t.Context(), "irrelevant", "n"); err == nil {
			t.Fatal("expected error when jwksURL is empty")
		}
	})

	if atomic.LoadInt32(hits) == 0 {
		t.Error("expected JWKS endpoint to be hit at least once")
	}
}

func TestCachedJWKS(t *testing.T) {
	kp := newTestKeyPair(t, "kid-1")
	srv, hits := jwksTestServer(t, kp.pubSet)

	p := &OIDCProvider{issuerURL: "https://idp.example.com", clientID: "client-123", jwksURL: srv.URL}

	set1, err := p.cachedJWKS(t.Context())
	if err != nil {
		t.Fatalf("cachedJWKS: %v", err)
	}
	hitsAfterFirst := atomic.LoadInt32(hits)
	if hitsAfterFirst != 1 {
		t.Fatalf("expected 1 fetch, got %d", hitsAfterFirst)
	}

	set2, err := p.cachedJWKS(t.Context())
	if err != nil {
		t.Fatalf("cachedJWKS: %v", err)
	}
	if atomic.LoadInt32(hits) != hitsAfterFirst {
		t.Errorf("expected cached result to avoid refetch, fetch count went from %d to %d", hitsAfterFirst, atomic.LoadInt32(hits))
	}
	if set1 != set2 {
		t.Error("expected the same cached jwk.Set instance to be returned")
	}

	// Force a refresh by resetting the fetch timestamp to outside the TTL window.
	p.jwksMu.Lock()
	p.jwksFetchedAt = time.Now().Add(-16 * time.Minute)
	p.jwksMu.Unlock()

	if _, err := p.cachedJWKS(t.Context()); err != nil {
		t.Fatalf("cachedJWKS after expiry: %v", err)
	}
	if atomic.LoadInt32(hits) != hitsAfterFirst+1 {
		t.Errorf("expected refetch after TTL expiry, fetch count = %d", atomic.LoadInt32(hits))
	}
}

func TestCachedJWKSStaleFallbackOnFetchError(t *testing.T) {
	kp := newTestKeyPair(t, "kid-1")

	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(kp.pubSet)
	}))
	t.Cleanup(srv.Close)

	p := &OIDCProvider{issuerURL: "https://idp.example.com", clientID: "client-123", jwksURL: srv.URL}

	// Prime the cache with a successful fetch.
	if _, err := p.cachedJWKS(t.Context()); err != nil {
		t.Fatalf("initial cachedJWKS: %v", err)
	}

	// Force expiry, then make the IdP start failing.
	p.jwksMu.Lock()
	p.jwksFetchedAt = time.Now().Add(-16 * time.Minute)
	p.jwksMu.Unlock()
	fail.Store(true)

	set, err := p.cachedJWKS(t.Context())
	if err != nil {
		t.Fatalf("expected stale-cache fallback, got error: %v", err)
	}
	if set == nil {
		t.Fatal("expected stale set to be returned, got nil")
	}
}

func TestCachedJWKSErrorWithNoCache(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	p := &OIDCProvider{issuerURL: "https://idp.example.com", clientID: "client-123", jwksURL: srv.URL}
	if _, err := p.cachedJWKS(t.Context()); err == nil {
		t.Fatal("expected error when JWKS fetch fails and no cache exists")
	}
}
