package sso

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

// fakeRevocationStore is an in-memory RevocationStore for tests.
type fakeRevocationStore struct {
	mu      sync.Mutex
	revoked map[string]time.Time // jti -> expiry
}

func newFakeRevocationStore() *fakeRevocationStore {
	return &fakeRevocationStore{revoked: make(map[string]time.Time)}
}

func (s *fakeRevocationStore) Revoke(_ context.Context, jti string, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revoked[jti] = time.Now().Add(ttl)
	return nil
}

func (s *fakeRevocationStore) IsRevoked(_ context.Context, jti string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.revoked[jti]
	if !ok {
		return false
	}
	return time.Now().Before(exp)
}

func (s *fakeRevocationStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.revoked)
}

// newTestGinContext builds a gin.Context wired to a ResponseRecorder, with the
// given cookies attached to the inbound request.
func newTestGinContext(cookies ...*http.Cookie) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	c.Request = req
	return c, w
}

// extractCookie finds a cookie by name from the recorder's Set-Cookie headers.
func extractCookie(w *httptest.ResponseRecorder, name string) *http.Cookie {
	resp := http.Response{Header: w.Header()}
	for _, ck := range resp.Cookies() {
		if ck.Name == name {
			return ck
		}
	}
	return nil
}

func TestSessionSetAndGet(t *testing.T) {
	mgr := NewSessionManager([]byte("test-secret-must-be-long-enough!"), 24, true, nil)

	c, w := newTestGinContext()
	claims := Claims{APIKeyID: "key-123", Email: "user@example.com", Provider: "oidc"}
	if err := mgr.Set(c, claims); err != nil {
		t.Fatalf("Set: %v", err)
	}

	cookie := extractCookie(w, cookieName)
	if cookie == nil {
		t.Fatal("expected session cookie to be set")
	}
	if !cookie.HttpOnly {
		t.Error("expected HttpOnly cookie")
	}
	if !cookie.Secure {
		t.Error("expected Secure cookie when manager configured with secure=true")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", cookie.SameSite)
	}

	// Round-trip: parse the cookie back via Get.
	c2, _ := newTestGinContext(cookie)
	got, err := mgr.Get(c2)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected claims, got nil")
	}
	if got.APIKeyID != claims.APIKeyID || got.Email != claims.Email || got.Provider != claims.Provider {
		t.Errorf("got claims %+v, want %+v", got, claims)
	}
}

func TestSessionGetNoCookie(t *testing.T) {
	mgr := NewSessionManager([]byte("test-secret-must-be-long-enough!"), 24, true, nil)
	c, _ := newTestGinContext()
	got, err := mgr.Get(c)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil claims for missing cookie, got %+v", got)
	}
}

func TestSessionGetTamperedCookie(t *testing.T) {
	mgr := NewSessionManager([]byte("test-secret-must-be-long-enough!"), 24, true, nil)
	c, w := newTestGinContext()
	if err := mgr.Set(c, Claims{APIKeyID: "key-123"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	cookie := extractCookie(w, cookieName)

	// Flip a character in the signed token to invalidate the signature.
	tampered := *cookie
	if len(tampered.Value) > 10 {
		runes := []rune(tampered.Value)
		if runes[len(runes)-5] == 'a' {
			runes[len(runes)-5] = 'b'
		} else {
			runes[len(runes)-5] = 'a'
		}
		tampered.Value = string(runes)
	}

	c2, _ := newTestGinContext(&tampered)
	if _, err := mgr.Get(c2); err == nil {
		t.Fatal("expected error for tampered session cookie")
	}
}

func TestSessionGetWrongSecret(t *testing.T) {
	mgr := NewSessionManager([]byte("test-secret-must-be-long-enough!"), 24, true, nil)
	c, w := newTestGinContext()
	if err := mgr.Set(c, Claims{APIKeyID: "key-123"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	cookie := extractCookie(w, cookieName)

	other := NewSessionManager([]byte("a-completely-different-secret!!!"), 24, true, nil)
	c2, _ := newTestGinContext(cookie)
	if _, err := other.Get(c2); err == nil {
		t.Fatal("expected error when verifying with the wrong secret")
	}
}

func TestSessionGetExpired(t *testing.T) {
	mgr := NewSessionManager([]byte("test-secret-must-be-long-enough!"), 24, true, nil)

	// Build an already-expired token directly (Set always issues fresh tokens).
	now := time.Now()
	tok, err := jwt.NewBuilder().
		JwtID("expired-jti").
		IssuedAt(now.Add(-2 * time.Hour)).
		Expiration(now.Add(-time.Hour)).
		Claim("key_id", "key-123").
		Build()
	if err != nil {
		t.Fatalf("build token: %v", err)
	}
	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.HS256(), []byte("test-secret-must-be-long-enough!")))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	c, _ := newTestGinContext(&http.Cookie{Name: cookieName, Value: string(signed)})
	if _, err := mgr.Get(c); err == nil {
		t.Fatal("expected error for expired session token")
	}
}

func TestSessionRevocation(t *testing.T) {
	store := newFakeRevocationStore()
	mgr := NewSessionManager([]byte("test-secret-must-be-long-enough!"), 24, true, store)

	c, w := newTestGinContext()
	if err := mgr.Set(c, Claims{APIKeyID: "key-123", Provider: "oidc"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	cookie := extractCookie(w, cookieName)

	// Session is valid before logout.
	c2, _ := newTestGinContext(cookie)
	if _, err := mgr.Get(c2); err != nil {
		t.Fatalf("expected valid session before logout, got: %v", err)
	}

	// Clear (logout) revokes the jti server-side and deletes the cookie.
	c3, w3 := newTestGinContext(cookie)
	mgr.Clear(c3)

	cleared := extractCookie(w3, cookieName)
	if cleared == nil {
		t.Fatal("expected Clear to set a cookie deletion header")
	}
	if cleared.MaxAge >= 0 {
		t.Errorf("expected MaxAge < 0 to delete cookie, got %d", cleared.MaxAge)
	}
	if store.count() != 1 {
		t.Fatalf("expected 1 revoked jti, got %d", store.count())
	}

	// The original (now revoked) cookie must be rejected even though its
	// signature and expiry are still valid.
	c4, _ := newTestGinContext(cookie)
	if _, err := mgr.Get(c4); err == nil {
		t.Fatal("expected error for revoked session")
	}
}

func TestSessionClearWithoutStore(t *testing.T) {
	mgr := NewSessionManager([]byte("test-secret-must-be-long-enough!"), 24, true, nil)
	c, w := newTestGinContext()
	if err := mgr.Set(c, Claims{APIKeyID: "key-123"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	cookie := extractCookie(w, cookieName)

	c2, w2 := newTestGinContext(cookie)
	mgr.Clear(c2)

	cleared := extractCookie(w2, cookieName)
	if cleared == nil || cleared.MaxAge >= 0 {
		t.Fatal("expected Clear to delete the cookie even without a revocation store")
	}

	// Without a store, the original token remains valid until natural expiry —
	// Get should still succeed (no server-side revocation is possible).
	c3, _ := newTestGinContext(cookie)
	if _, err := mgr.Get(c3); err != nil {
		t.Fatalf("expected token to remain valid without a revocation store: %v", err)
	}
}

func TestSessionSecureFlag(t *testing.T) {
	secure := NewSessionManager([]byte("test-secret-must-be-long-enough!"), 24, true, nil)
	if !secure.Secure() {
		t.Error("expected Secure() to be true")
	}

	insecure := NewSessionManager([]byte("test-secret-must-be-long-enough!"), 24, false, nil)
	if insecure.Secure() {
		t.Error("expected Secure() to be false")
	}

	c, w := newTestGinContext()
	if err := insecure.Set(c, Claims{APIKeyID: "key-123"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	cookie := extractCookie(w, cookieName)
	if cookie.Secure {
		t.Error("expected cookie Secure flag to follow manager configuration")
	}
}

func TestSessionMissingKeyIDClaim(t *testing.T) {
	mgr := NewSessionManager([]byte("test-secret-must-be-long-enough!"), 24, true, nil)

	now := time.Now()
	tok, err := jwt.NewBuilder().
		JwtID("some-jti").
		IssuedAt(now).
		Expiration(now.Add(time.Hour)).
		Claim("email", "user@example.com").
		Build()
	if err != nil {
		t.Fatalf("build token: %v", err)
	}
	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.HS256(), []byte("test-secret-must-be-long-enough!")))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	c, _ := newTestGinContext(&http.Cookie{Name: cookieName, Value: string(signed)})
	if _, err := mgr.Get(c); err == nil {
		t.Fatal("expected error when key_id claim is missing")
	}
}

func TestNewJTIUnique(t *testing.T) {
	a, err := newJTI()
	if err != nil {
		t.Fatalf("newJTI: %v", err)
	}
	b, err := newJTI()
	if err != nil {
		t.Fatalf("newJTI: %v", err)
	}
	if a == b {
		t.Fatal("expected unique jti values")
	}
	if len(a) != 32 { // 16 bytes hex-encoded
		t.Errorf("jti length = %d, want 32", len(a))
	}
}

func TestAsString(t *testing.T) {
	if got := asString("hello"); got != "hello" {
		t.Errorf("asString(string) = %q", got)
	}
	if got := asString(123); got != "" {
		t.Errorf("asString(int) = %q, want empty", got)
	}
}

func TestNewSessionManagerDefaultMaxAge(t *testing.T) {
	mgr := NewSessionManager([]byte("test-secret-must-be-long-enough!"), 0, true, nil)
	if mgr.maxAge != 24*time.Hour {
		t.Errorf("expected default max age of 24h, got %v", mgr.maxAge)
	}
}
