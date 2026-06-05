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

	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

// OIDCProvider performs an OIDC Authorization Code flow with PKCE.
// It performs discovery on startup and caches the IdP's endpoint URLs.
type OIDCProvider struct {
	issuerURL    string
	clientID     string
	clientSecret string
	redirectURL  string
	scopes       []string

	// Populated by discover()
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
	if doc.JWKsURI == "" {
		return errors.New("discovery doc missing jwks_uri — cannot verify ID tokens")
	}
	p.authURL = doc.AuthorizationEndpoint
	p.tokenURL = doc.TokenEndpoint
	p.jwksURL = doc.JWKsURI
	return nil
}

// AuthCodeURL returns the URL to redirect the user to for OIDC login.
// state prevents CSRF; codeVerifier is the PKCE verifier; nonce prevents
// ID token replay (OIDC Core §3.1.2.1).
func (p *OIDCProvider) AuthCodeURL(state, codeVerifier, nonce string) string {
	challenge := pkceChallenge(codeVerifier)
	v := url.Values{}
	v.Set("response_type", "code")
	v.Set("client_id", p.clientID)
	v.Set("redirect_uri", p.redirectURL)
	v.Set("scope", strings.Join(p.scopes, " "))
	v.Set("state", state)
	v.Set("code_challenge", challenge)
	v.Set("code_challenge_method", "S256")
	v.Set("nonce", nonce)
	return p.authURL + "?" + v.Encode()
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	TokenType   string `json:"token_type"`
}

// Exchange swaps an authorization code for an ID token and returns the claims.
// nonce must match the value sent in AuthCodeURL to prevent token replay.
func (p *OIDCProvider) Exchange(ctx context.Context, code, codeVerifier, nonce string) (*IDTokenClaims, error) {
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

	return p.verifyIDToken(ctx, tr.IDToken, nonce)
}

// verifyIDToken fetches the IdP's JWKS, verifies the ID token signature,
// and validates iss, aud, exp, and nonce claims.
func (p *OIDCProvider) verifyIDToken(ctx context.Context, raw, nonce string) (*IDTokenClaims, error) {
	if p.jwksURL == "" {
		return nil, errors.New("JWKS URL not available; cannot verify ID token")
	}

	keySet, err := jwk.Fetch(ctx, p.jwksURL)
	if err != nil {
		return nil, fmt.Errorf("fetch JWKS from %s: %w", p.jwksURL, err)
	}

	tok, err := jwt.Parse([]byte(raw),
		jwt.WithKeySet(keySet),
		jwt.WithValidate(true),
	)
	if err != nil {
		return nil, fmt.Errorf("id_token verification failed: %w", err)
	}

	// Validate issuer
	var issVal any
	_ = tok.Get("iss", &issVal)
	iss, _ := issVal.(string)
	if iss != p.issuerURL {
		return nil, fmt.Errorf("id_token iss %q does not match expected issuer", iss)
	}

	// Validate audience — aud may be a string or []string
	var audVal any
	_ = tok.Get("aud", &audVal)
	if !audienceContains(audVal, p.clientID) {
		return nil, errors.New("id_token aud does not contain client_id")
	}

	// Validate nonce — prevents ID token replay at the token endpoint
	if nonce != "" {
		var nonceVal any
		_ = tok.Get("nonce", &nonceVal)
		if got, _ := nonceVal.(string); got != nonce {
			return nil, errors.New("id_token nonce mismatch")
		}
	}

	var subVal any
	_ = tok.Get("sub", &subVal)
	sub, _ := subVal.(string)
	if sub == "" {
		return nil, errors.New("id_token missing sub claim")
	}

	var emailVal, nameVal any
	_ = tok.Get("email", &emailVal)
	_ = tok.Get("name", &nameVal)

	return &IDTokenClaims{
		Sub:   sub,
		Email: claimString(emailVal),
		Name:  claimString(nameVal),
	}, nil
}

// audienceContains checks whether clientID appears in a JWT aud claim,
// which may be a string, []string, or []any after JSON parsing.
func audienceContains(aud any, clientID string) bool {
	switch v := aud.(type) {
	case string:
		return v == clientID
	case []string:
		for _, a := range v {
			if a == clientID {
				return true
			}
		}
	case []any:
		for _, a := range v {
			if s, ok := a.(string); ok && s == clientID {
				return true
			}
		}
	}
	return false
}

func claimString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// GenerateState returns a cryptographically random state parameter (256 bits).
func GenerateState() (string, error) {
	b := make([]byte, 32)
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

// GenerateNonce returns a cryptographically random nonce for OIDC replay protection.
func GenerateNonce() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

func pkceChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
