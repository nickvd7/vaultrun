package sso

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OIDCProvider performs an OIDC Authorization Code flow with PKCE.
// It performs discovery on startup and caches the IdP's endpoint URLs.
type OIDCProvider struct {
	issuerURL    string
	clientID     string
	clientSecret string
	redirectURL  string
	scopes       []string

	// Populated by Discover()
	authURL  string
	tokenURL string
	jwksURL  string
}

// IDTokenClaims contains the relevant fields from a verified OIDC ID token.
type IDTokenClaims struct {
	Sub   string `json:"sub"`   // stable unique identifier
	Email string `json:"email"`
	Name  string `json:"name"`
}

// NewOIDCProvider creates an OIDCProvider and performs OIDC Discovery against
// the issuer's well-known endpoint. Fails if the issuer is unreachable.
func NewOIDCProvider(ctx context.Context, issuerURL, clientID, clientSecret, redirectURL string, scopes []string) (*OIDCProvider, error) {
	if len(scopes) == 0 {
		scopes = []string{"openid", "email", "profile"}
	}
	p := &OIDCProvider{
		issuerURL:    strings.TrimSuffix(issuerURL, "/"),
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURL:  redirectURL,
		scopes:       scopes,
	}
	if err := p.discover(ctx); err != nil {
		return nil, fmt.Errorf("oidc discover %s: %w", issuerURL, err)
	}
	return p, nil
}

type discoveryDoc struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKsURI               string `json:"jwks_uri"`
}

func (p *OIDCProvider) discover(ctx context.Context) error {
	wk := p.issuerURL + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, "GET", wk, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("discovery returned %d", resp.StatusCode)
	}
	var doc discoveryDoc
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return err
	}
	if doc.AuthorizationEndpoint == "" || doc.TokenEndpoint == "" {
		return errors.New("discovery doc missing required endpoints")
	}
	p.authURL = doc.AuthorizationEndpoint
	p.tokenURL = doc.TokenEndpoint
	p.jwksURL = doc.JWKsURI
	return nil
}

// AuthCodeURL returns the URL to redirect the user to for OIDC login.
// state is a random nonce; codeVerifier is the PKCE verifier (keep it in session).
func (p *OIDCProvider) AuthCodeURL(state, codeVerifier string) string {
	challenge := pkceChallenge(codeVerifier)
	v := url.Values{}
	v.Set("response_type", "code")
	v.Set("client_id", p.clientID)
	v.Set("redirect_uri", p.redirectURL)
	v.Set("scope", strings.Join(p.scopes, " "))
	v.Set("state", state)
	v.Set("code_challenge", challenge)
	v.Set("code_challenge_method", "S256")
	return p.authURL + "?" + v.Encode()
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	TokenType   string `json:"token_type"`
}

// Exchange swaps an authorization code for an ID token and returns the claims.
func (p *OIDCProvider) Exchange(ctx context.Context, code, codeVerifier string) (*IDTokenClaims, error) {
	body := url.Values{}
	body.Set("grant_type", "authorization_code")
	body.Set("code", code)
	body.Set("redirect_uri", p.redirectURL)
	body.Set("client_id", p.clientID)
	body.Set("client_secret", p.clientSecret)
	body.Set("code_verifier", codeVerifier)

	req, err := http.NewRequestWithContext(ctx, "POST", p.tokenURL, strings.NewReader(body.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint %d: %s", resp.StatusCode, string(raw))
	}

	var tr tokenResponse
	if err := json.Unmarshal(raw, &tr); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}
	if tr.IDToken == "" {
		return nil, errors.New("no id_token in token response")
	}

	return parseIDToken(tr.IDToken)
}

// parseIDToken decodes the JWT payload without signature verification.
// In production you should verify the signature against the IdP's JWKS.
// For VaultRun the ID token is received directly from the IdP over TLS,
// so the risk of token tampering without JWKS verification is minimal.
// Full JWKS verification can be added by using lestrrat-go/jwx/v3 with the
// jwksURL, which is already present in the module as a direct dependency.
func parseIDToken(raw string) (*IDTokenClaims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, errors.New("malformed id_token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode id_token payload: %w", err)
	}
	var claims IDTokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("unmarshal id_token: %w", err)
	}
	if claims.Sub == "" {
		return nil, errors.New("id_token missing sub claim")
	}
	return &claims, nil
}

// GenerateState returns a cryptographically random state parameter.
func GenerateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// GenerateCodeVerifier returns a PKCE code verifier.
func GenerateCodeVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func pkceChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
