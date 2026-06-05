// Package sso provides OIDC and SAML SSO support for VaultRun.
// After a successful SSO flow the server issues an encrypted JWT session
// cookie that maps back to an existing VaultRun API key.
package sso

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

const cookieName = "vaultrun_session"

// SessionManager issues and validates signed JWT session cookies.
type SessionManager struct {
	secret []byte
	maxAge time.Duration
	secure bool // set false only in local dev (no TLS)
}

// Claims embedded in the session JWT.
type Claims struct {
	APIKeyID string // the VaultRun API key to use for subsequent requests
	Email    string
	Provider string // "oidc" or "saml"
}

func NewSessionManager(secret []byte, maxAgeHours int, secure bool) *SessionManager {
	if maxAgeHours <= 0 {
		maxAgeHours = 24
	}
	return &SessionManager{
		secret: secret,
		maxAge: time.Duration(maxAgeHours) * time.Hour,
		secure: secure,
	}
}

// Secure returns whether the Secure flag is set on cookies (reflects TLS state).
func (m *SessionManager) Secure() bool { return m.secure }

// Set writes a signed JWT session cookie to the response.
// SameSite=Lax prevents the cookie from being sent on cross-site subresource
// requests while still allowing it on top-level navigations (e.g. OIDC redirects).
func (m *SessionManager) Set(c *gin.Context, claims Claims) error {
	now := time.Now()
	tok, err := jwt.NewBuilder().
		IssuedAt(now).
		Expiration(now.Add(m.maxAge)).
		Claim("key_id", claims.APIKeyID).
		Claim("email", claims.Email).
		Claim("provider", claims.Provider).
		Build()
	if err != nil {
		return err
	}

	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.HS256(), m.secret))
	if err != nil {
		return err
	}

	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cookieName,
		Value:    string(signed),
		MaxAge:   int(m.maxAge.Seconds()),
		Path:     "/",
		Secure:   m.secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// Get validates the session cookie and returns the claims. Returns nil, nil
// when no session cookie is present.
func (m *SessionManager) Get(c *gin.Context) (*Claims, error) {
	raw, err := c.Cookie(cookieName)
	if err != nil {
		if errors.Is(err, http.ErrNoCookie) {
			return nil, nil
		}
		return nil, err
	}
	if raw == "" {
		return nil, nil
	}

	tok, err := jwt.Parse([]byte(raw), jwt.WithKey(jwa.HS256(), m.secret), jwt.WithValidate(true))
	if err != nil {
		return nil, err
	}

	var keyID, email, provider any
	_ = tok.Get("key_id", &keyID)
	_ = tok.Get("email", &email)
	_ = tok.Get("provider", &provider)

	kid, _ := keyID.(string)
	if kid == "" {
		return nil, errors.New("session: missing key_id claim")
	}

	return &Claims{
		APIKeyID: kid,
		Email:    asString(email),
		Provider: asString(provider),
	}, nil
}

// Clear deletes the session cookie.
func (m *SessionManager) Clear(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		MaxAge:   -1,
		Path:     "/",
		Secure:   m.secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
