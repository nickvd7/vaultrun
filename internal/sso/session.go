// Package sso provides OIDC and SAML SSO support for VaultRun.
// After a successful SSO flow the server issues an encrypted JWT session
// cookie that maps back to an existing VaultRun API key.
package sso

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	secure bool  // set false only in local dev (no TLS)
	store  RevocationStore // nil = no server-side revocation (logout still clears cookie)
}

// Claims embedded in the session JWT.
type Claims struct {
	APIKeyID string // the VaultRun API key to use for subsequent requests
	Email    string
	Provider string // "oidc" or "saml"
}

func NewSessionManager(secret []byte, maxAgeHours int, secure bool, store RevocationStore) *SessionManager {
	if maxAgeHours <= 0 {
		maxAgeHours = 24
	}
	return &SessionManager{
		secret: secret,
		maxAge: time.Duration(maxAgeHours) * time.Hour,
		secure: secure,
		store:  store,
	}
}

// Secure returns whether the Secure flag is set on cookies (reflects TLS state).
func (m *SessionManager) Secure() bool { return m.secure }

// Set writes a signed JWT session cookie to the response.
// Each session carries a unique jti for server-side revocation via Clear.
// SameSite=Lax prevents the cookie from being sent on cross-site subresource
// requests while allowing it on top-level navigations (e.g. OIDC redirects).
func (m *SessionManager) Set(c *gin.Context, claims Claims) error {
	jti, err := newJTI()
	if err != nil {
		return err
	}
	now := time.Now()
	tok, err := jwt.NewBuilder().
		JwtID(jti).
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
// when no session cookie is present or the session has been revoked.
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

	// Check server-side revocation via jti
	if m.store != nil {
		var jtiVal any
		_ = tok.Get("jti", &jtiVal)
		if jti, _ := jtiVal.(string); jti != "" {
			if m.store.IsRevoked(c.Request.Context(), jti) {
				return nil, errors.New("session has been revoked")
			}
		}
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

// Clear revokes the current session server-side (when a revocation store is
// configured) and deletes the session cookie.
func (m *SessionManager) Clear(c *gin.Context) {
	// Revoke the active JWT so it cannot be reused until its natural expiry.
	if m.store != nil {
		if raw, err := c.Cookie(cookieName); err == nil && raw != "" {
			if tok, err := jwt.Parse([]byte(raw),
				jwt.WithKey(jwa.HS256(), m.secret),
				jwt.WithValidate(false),
			); err == nil {
				var jtiVal any
				_ = tok.Get("jti", &jtiVal)
				if jti, _ := jtiVal.(string); jti != "" {
					exp, _ := tok.Expiration()
					remaining := time.Until(exp)
					if remaining > 0 {
						_ = m.store.Revoke(
							context.Background(),
							jti,
							remaining,
						)
					}
				}
			}
		}
	}

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

// newJTI generates a 128-bit random JWT ID (jti).
func newJTI() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
