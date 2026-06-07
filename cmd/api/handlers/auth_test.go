package handlers

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nickvd7/vaultrun/internal/sso"
)

// ── test fixtures ────────────────────────────────────────────────────────────

// newAuthTestContext builds a gin.Context backed by a ResponseRecorder for the
// given method/path/query, with optional cookies, headers, and body attached
// to the inbound request.
func newAuthTestContext(t *testing.T, method, target string, cookies []*http.Cookie, headers map[string]string, body string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	var bodyReader *strings.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	} else {
		bodyReader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, target, bodyReader)
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	c.Request = req
	return c, w
}

// cookiesFromRecorder parses Set-Cookie headers from a recorder into a map by name.
func cookiesFromRecorder(w *httptest.ResponseRecorder) map[string]*http.Cookie {
	resp := http.Response{Header: w.Header()}
	out := make(map[string]*http.Cookie)
	for _, ck := range resp.Cookies() {
		out[ck.Name] = ck
	}
	return out
}

// newDiscoveryOnlyOIDCProvider stands up a minimal fake IdP exposing OIDC
// discovery, JWKS, and a token endpoint whose behavior is controlled by
// tokenHandler. This is sufficient to exercise OIDCLogin (only needs the
// discovered authorization_endpoint) and the OIDCCallback paths that run
// before/around the token exchange.
func newTestOIDCProvider(t *testing.T, tokenHandler http.HandlerFunc) *sso.OIDCProvider {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"authorization_endpoint":%q,"token_endpoint":%q,"jwks_uri":%q}`,
			srv.URL+"/authorize", srv.URL+"/token", srv.URL+"/jwks")
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"keys":[]}`))
	})
	if tokenHandler != nil {
		mux.HandleFunc("/token", tokenHandler)
	} else {
		mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
	}

	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	p, err := sso.NewOIDCProvider(t.Context(), srv.URL, "client-123", "client-secret",
		"https://app.example.com/auth/oidc/callback", nil)
	if err != nil {
		t.Fatalf("NewOIDCProvider: %v", err)
	}
	return p
}

// generateTestCertKeyPEM writes a self-signed RSA certificate and key to PEM
// files in a temp directory and returns their paths, suitable for SAML SP
// signing/encryption in tests.
func generateTestCertKeyPEM(t *testing.T) (certPath, keyPath string) {
	t.Helper()
	dir := t.TempDir()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-sp"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")

	cf, err := os.Create(certPath)
	if err != nil {
		t.Fatalf("create cert file: %v", err)
	}
	if err := pem.Encode(cf, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		t.Fatalf("encode cert: %v", err)
	}
	_ = cf.Close()

	kf, err := os.Create(keyPath)
	if err != nil {
		t.Fatalf("create key file: %v", err)
	}
	if err := pem.Encode(kf, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)}); err != nil {
		t.Fatalf("encode key: %v", err)
	}
	_ = kf.Close()

	return certPath, keyPath
}

// newTestSAMLProvider builds a real *sso.SAMLProvider backed by a generated SP
// certificate/key and the bundled IdP metadata fixture (testdata/idp_metadata.xml).
func newTestSAMLProvider(t *testing.T) *sso.SAMLProvider {
	t.Helper()
	certPath, keyPath := generateTestCertKeyPEM(t)
	p, err := sso.NewSAMLProvider(t.Context(), "https://app.example.com", "", "", "testdata/idp_metadata.xml", certPath, keyPath)
	if err != nil {
		t.Fatalf("NewSAMLProvider: %v", err)
	}
	return p
}

func newTestSessionManager() *sso.SessionManager {
	return sso.NewSessionManager([]byte("test-secret-must-be-long-enough!"), 24, true, nil)
}

// ── OIDCLogin ────────────────────────────────────────────────────────────────

func TestOIDCLoginNotConfigured(t *testing.T) {
	h := &AuthHandler{oidc: nil}
	c, w := newAuthTestContext(t, http.MethodGet, "/auth/oidc/login", nil, nil, "")

	h.OIDCLogin(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestOIDCLoginSetsCookiesAndRedirects(t *testing.T) {
	provider := newTestOIDCProvider(t, nil)
	h := &AuthHandler{oidc: provider, secure: true}
	c, w := newAuthTestContext(t, http.MethodGet, "/auth/oidc/login", nil, nil, "")

	h.OIDCLogin(c)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
	}

	cookies := cookiesFromRecorder(w)
	for _, name := range []string{"oidc_state", "oidc_verifier", "oidc_nonce"} {
		ck, ok := cookies[name]
		if !ok {
			t.Fatalf("expected cookie %q to be set", name)
		}
		if !ck.HttpOnly {
			t.Errorf("cookie %q: expected HttpOnly", name)
		}
		if !ck.Secure {
			t.Errorf("cookie %q: expected Secure when handler configured secure=true", name)
		}
		if ck.SameSite != http.SameSiteStrictMode {
			t.Errorf("cookie %q: SameSite = %v, want Strict", name, ck.SameSite)
		}
		if ck.Path != "/auth/oidc" {
			t.Errorf("cookie %q: Path = %q, want /auth/oidc", name, ck.Path)
		}
		if ck.MaxAge != 600 {
			t.Errorf("cookie %q: MaxAge = %d, want 600", name, ck.MaxAge)
		}
		if ck.Value == "" {
			t.Errorf("cookie %q: expected non-empty value", name)
		}
	}

	loc := w.Result().Header.Get("Location")
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse redirect location: %v", err)
	}
	q := u.Query()
	if q.Get("state") != cookies["oidc_state"].Value {
		t.Errorf("redirect state %q does not match cookie value %q", q.Get("state"), cookies["oidc_state"].Value)
	}
	if q.Get("nonce") != cookies["oidc_nonce"].Value {
		t.Errorf("redirect nonce %q does not match cookie value %q", q.Get("nonce"), cookies["oidc_nonce"].Value)
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", q.Get("code_challenge_method"))
	}
	if q.Get("code_challenge") == "" {
		t.Error("expected non-empty code_challenge")
	}
}

func TestOIDCLoginGeneratesUniqueValuesPerRequest(t *testing.T) {
	provider := newTestOIDCProvider(t, nil)
	h := &AuthHandler{oidc: provider, secure: true}

	c1, w1 := newAuthTestContext(t, http.MethodGet, "/auth/oidc/login", nil, nil, "")
	h.OIDCLogin(c1)
	c2, w2 := newAuthTestContext(t, http.MethodGet, "/auth/oidc/login", nil, nil, "")
	h.OIDCLogin(c2)

	cookies1 := cookiesFromRecorder(w1)
	cookies2 := cookiesFromRecorder(w2)
	for _, name := range []string{"oidc_state", "oidc_verifier", "oidc_nonce"} {
		if cookies1[name].Value == cookies2[name].Value {
			t.Errorf("expected unique %q across requests (CSRF/replay protection)", name)
		}
	}
}

// ── OIDCCallback ─────────────────────────────────────────────────────────────

func TestOIDCCallbackNotConfigured(t *testing.T) {
	h := &AuthHandler{oidc: nil}
	c, w := newAuthTestContext(t, http.MethodGet, "/auth/oidc/callback?state=x&code=y", nil, nil, "")

	h.OIDCCallback(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestOIDCCallbackMissingStateCookie(t *testing.T) {
	provider := newTestOIDCProvider(t, nil)
	h := &AuthHandler{oidc: provider, secure: true}
	// No oidc_state cookie present, but the IdP supplied a state in the query.
	c, w := newAuthTestContext(t, http.MethodGet, "/auth/oidc/callback?state=abc&code=somecode", nil, nil, "")

	h.OIDCCallback(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestOIDCCallbackStateMismatchRejected(t *testing.T) {
	provider := newTestOIDCProvider(t, nil)
	h := &AuthHandler{oidc: provider, secure: true}
	cookies := []*http.Cookie{
		{Name: "oidc_state", Value: "cookie-state-value"},
		{Name: "oidc_verifier", Value: "verifier"},
		{Name: "oidc_nonce", Value: "nonce"},
	}
	c, w := newAuthTestContext(t, http.MethodGet, "/auth/oidc/callback?state=different-state&code=somecode", cookies, nil, "")

	h.OIDCCallback(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (CSRF state mismatch must be rejected)", w.Code, http.StatusBadRequest)
	}
}

func TestOIDCCallbackClearsPreAuthCookies(t *testing.T) {
	provider := newTestOIDCProvider(t, nil)
	h := &AuthHandler{oidc: provider, secure: true}
	cookies := []*http.Cookie{
		{Name: "oidc_state", Value: "match-me"},
		{Name: "oidc_verifier", Value: "verifier"},
		{Name: "oidc_nonce", Value: "nonce"},
	}
	// State matches but no code is supplied — request fails after cookies are cleared.
	c, w := newAuthTestContext(t, http.MethodGet, "/auth/oidc/callback?state=match-me", cookies, nil, "")

	h.OIDCCallback(c)

	cleared := cookiesFromRecorder(w)
	for _, name := range []string{"oidc_state", "oidc_verifier", "oidc_nonce"} {
		ck, ok := cleared[name]
		if !ok {
			t.Fatalf("expected %q to be cleared via Set-Cookie", name)
		}
		if ck.MaxAge >= 0 {
			t.Errorf("%q: MaxAge = %d, want < 0 (deletion)", name, ck.MaxAge)
		}
		if ck.SameSite != http.SameSiteStrictMode {
			t.Errorf("%q: deletion cookie SameSite = %v, want Strict (must match creation flags)", name, ck.SameSite)
		}
	}
}

func TestOIDCCallbackMissingCodeRejected(t *testing.T) {
	provider := newTestOIDCProvider(t, nil)
	h := &AuthHandler{oidc: provider, secure: true}
	cookies := []*http.Cookie{
		{Name: "oidc_state", Value: "match-me"},
		{Name: "oidc_verifier", Value: "verifier"},
		{Name: "oidc_nonce", Value: "nonce"},
	}
	c, w := newAuthTestContext(t, http.MethodGet, "/auth/oidc/callback?state=match-me", cookies, nil, "")

	h.OIDCCallback(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestOIDCCallbackIdPErrorNotReflected(t *testing.T) {
	provider := newTestOIDCProvider(t, nil)
	h := &AuthHandler{oidc: provider, secure: true}
	cookies := []*http.Cookie{
		{Name: "oidc_state", Value: "match-me"},
		{Name: "oidc_verifier", Value: "verifier"},
		{Name: "oidc_nonce", Value: "nonce"},
	}
	maliciousDescription := "<script>alert(1)</script> attacker controlled text"
	target := "/auth/oidc/callback?state=match-me&error=access_denied&error_description=" + url.QueryEscape(maliciousDescription)
	c, w := newAuthTestContext(t, http.MethodGet, target, cookies, nil, "")

	h.OIDCCallback(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	body := w.Body.String()
	if strings.Contains(body, maliciousDescription) || strings.Contains(body, "access_denied") {
		t.Errorf("attacker-controlled IdP error fields must not be reflected in the response body, got: %s", body)
	}
}

func TestOIDCCallbackExchangeFailureReturns500(t *testing.T) {
	// Token endpoint always errors — Exchange must fail before any DB access
	// (upsertSSOUser is only reached on success).
	provider := newTestOIDCProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"server_error"}`))
	})
	h := &AuthHandler{oidc: provider, secure: true}
	cookies := []*http.Cookie{
		{Name: "oidc_state", Value: "match-me"},
		{Name: "oidc_verifier", Value: "verifier"},
		{Name: "oidc_nonce", Value: "nonce"},
	}
	c, w := newAuthTestContext(t, http.MethodGet, "/auth/oidc/callback?state=match-me&code=auth-code-123", cookies, nil, "")

	h.OIDCCallback(c)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

// ── SAML metadata / login ────────────────────────────────────────────────────

func TestSAMLMetadataNotConfigured(t *testing.T) {
	h := &AuthHandler{saml: nil}
	c, w := newAuthTestContext(t, http.MethodGet, "/auth/saml/metadata", nil, nil, "")

	h.SAMLMetadata(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestSAMLMetadataServesXML(t *testing.T) {
	h := &AuthHandler{saml: newTestSAMLProvider(t)}
	c, w := newAuthTestContext(t, http.MethodGet, "/auth/saml/metadata", nil, nil, "")

	h.SAMLMetadata(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "samlmetadata+xml") {
		t.Errorf("Content-Type = %q, want samlmetadata+xml", ct)
	}
	if !strings.Contains(w.Body.String(), "EntityDescriptor") {
		t.Error("expected metadata XML to contain EntityDescriptor")
	}
}

func TestSAMLLoginNotConfigured(t *testing.T) {
	h := &AuthHandler{saml: nil}
	c, w := newAuthTestContext(t, http.MethodGet, "/auth/saml/login", nil, nil, "")

	h.SAMLLogin(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestSAMLLoginSetsRequestIDCookieAndRedirects(t *testing.T) {
	h := &AuthHandler{saml: newTestSAMLProvider(t), secure: true}
	c, w := newAuthTestContext(t, http.MethodGet, "/auth/saml/login", nil, nil, "")

	h.SAMLLogin(c)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
	}
	cookies := cookiesFromRecorder(w)
	ck, ok := cookies["saml_request_id"]
	if !ok {
		t.Fatal("expected saml_request_id cookie to be set")
	}
	if ck.Value == "" {
		t.Error("expected non-empty AuthnRequest ID")
	}
	if !ck.HttpOnly || !ck.Secure {
		t.Error("expected HttpOnly+Secure saml_request_id cookie")
	}
	if ck.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite = %v, want Strict", ck.SameSite)
	}
	if ck.Path != "/auth/saml" {
		t.Errorf("Path = %q, want /auth/saml", ck.Path)
	}
	if loc := w.Result().Header.Get("Location"); loc == "" {
		t.Error("expected redirect Location header")
	}
}

func TestSAMLLoginGeneratesUniqueRequestIDs(t *testing.T) {
	provider := newTestSAMLProvider(t)
	h := &AuthHandler{saml: provider, secure: true}

	c1, w1 := newAuthTestContext(t, http.MethodGet, "/auth/saml/login", nil, nil, "")
	h.SAMLLogin(c1)
	c2, w2 := newAuthTestContext(t, http.MethodGet, "/auth/saml/login", nil, nil, "")
	h.SAMLLogin(c2)

	id1 := cookiesFromRecorder(w1)["saml_request_id"].Value
	id2 := cookiesFromRecorder(w2)["saml_request_id"].Value
	if id1 == id2 {
		t.Error("expected unique AuthnRequest IDs across logins (replay protection)")
	}
}

// ── SAML ACS ─────────────────────────────────────────────────────────────────

func TestSAMLACSNotConfigured(t *testing.T) {
	h := &AuthHandler{saml: nil}
	c, w := newAuthTestContext(t, http.MethodPost, "/auth/saml/acs", nil,
		map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, "SAMLResponse=x")

	h.SAMLACS(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestSAMLACSRejectsWrongContentType(t *testing.T) {
	h := &AuthHandler{saml: newTestSAMLProvider(t), secure: true}

	for _, ct := range []string{"application/json", "multipart/form-data", "text/plain", ""} {
		t.Run(ct, func(t *testing.T) {
			headers := map[string]string{}
			if ct != "" {
				headers["Content-Type"] = ct
			}
			c, w := newAuthTestContext(t, http.MethodPost, "/auth/saml/acs", nil, headers, `{"SAMLResponse":"x"}`)

			h.SAMLACS(c)

			if w.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("Content-Type %q: status = %d, want %d", ct, w.Code, http.StatusUnsupportedMediaType)
			}
		})
	}
}

func TestSAMLACSInvalidResponseRejectedAndCookieCleared(t *testing.T) {
	h := &AuthHandler{saml: newTestSAMLProvider(t), secure: true}
	cookies := []*http.Cookie{{Name: "saml_request_id", Value: "id-original-request"}}
	headers := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	c, w := newAuthTestContext(t, http.MethodPost, "/auth/saml/acs", cookies, headers, "SAMLResponse=not-a-valid-saml-response")

	h.SAMLACS(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	cleared := cookiesFromRecorder(w)
	ck, ok := cleared["saml_request_id"]
	if !ok {
		t.Fatal("expected saml_request_id cookie to be cleared")
	}
	if ck.MaxAge >= 0 {
		t.Errorf("MaxAge = %d, want < 0 (deletion)", ck.MaxAge)
	}
}

// ── Me / Logout ──────────────────────────────────────────────────────────────

func TestMeRequiresSSOSession(t *testing.T) {
	tests := []struct {
		name  string
		actor string
		set   bool
	}{
		{"no actor set", "", false},
		{"empty actor", "", true},
		{"unknown actor (api-key/master auth, not SSO)", "unknown", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := &AuthHandler{}
			c, w := newAuthTestContext(t, http.MethodGet, "/auth/me", nil, nil, "")
			if tc.set {
				c.Set("actor", tc.actor)
			}

			h.Me(c)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestLogoutClearsSessionCookie(t *testing.T) {
	mgr := newTestSessionManager()
	h := &AuthHandler{session: mgr}

	// First, create a session cookie to log out from.
	setupCtx, setupRec := newAuthTestContext(t, http.MethodGet, "/", nil, nil, "")
	if err := mgr.Set(setupCtx, sso.Claims{APIKeyID: "key-123", Provider: "oidc"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	sessionCookie := cookiesFromRecorder(setupRec)["vaultrun_session"]

	c, w := newAuthTestContext(t, http.MethodPost, "/auth/logout", []*http.Cookie{sessionCookie}, nil, "")
	h.Logout(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	cleared := cookiesFromRecorder(w)["vaultrun_session"]
	if cleared == nil || cleared.MaxAge >= 0 {
		t.Fatal("expected Logout to delete the session cookie")
	}
}

func TestLogoutWithoutSessionManager(t *testing.T) {
	h := &AuthHandler{session: nil}
	c, w := newAuthTestContext(t, http.MethodPost, "/auth/logout", nil, nil, "")

	h.Logout(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (Logout must be safe to call when SSO is not configured)", w.Code, http.StatusOK)
	}
}
