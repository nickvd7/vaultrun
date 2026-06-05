package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/nickvd7/vaultrun/internal/audit"
	"github.com/nickvd7/vaultrun/internal/auth"
	"github.com/nickvd7/vaultrun/internal/models"
	"github.com/nickvd7/vaultrun/internal/sso"
)

// AuthHandler handles OIDC and SAML SSO flows.
type AuthHandler struct {
	db      *sqlx.DB
	oidc    *sso.OIDCProvider  // nil when OIDC is not configured
	saml    *sso.SAMLProvider  // nil when SAML is not configured
	session *sso.SessionManager
	audit   *audit.Logger
}

func NewAuthHandler(
	db *sqlx.DB,
	oidcProv *sso.OIDCProvider,
	samlProv *sso.SAMLProvider,
	sessionMgr *sso.SessionManager,
	auditLog *audit.Logger,
) *AuthHandler {
	return &AuthHandler{
		db:      db,
		oidc:    oidcProv,
		saml:    samlProv,
		session: sessionMgr,
		audit:   auditLog,
	}
}

// ── OIDC ─────────────────────────────────────────────────────────────────────

// OIDCLogin redirects the browser to the IdP's authorization endpoint.
func (h *AuthHandler) OIDCLogin(c *gin.Context) {
	if h.oidc == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "OIDC is not configured"})
		return
	}

	state, err := sso.GenerateState()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate state"})
		return
	}
	verifier, err := sso.GenerateCodeVerifier()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate PKCE verifier"})
		return
	}

	// Store state + verifier in short-lived cookies (HttpOnly; cleared after callback)
	c.SetCookie("oidc_state", state, 600, "/auth/oidc", "", h.session != nil, true)
	c.SetCookie("oidc_verifier", verifier, 600, "/auth/oidc", "", h.session != nil, true)

	c.Redirect(http.StatusFound, h.oidc.AuthCodeURL(state, verifier))
}

// OIDCCallback handles the authorization code callback from the IdP.
func (h *AuthHandler) OIDCCallback(c *gin.Context) {
	if h.oidc == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "OIDC is not configured"})
		return
	}

	// Validate state
	cookieState, _ := c.Cookie("oidc_state")
	if cookieState == "" || cookieState != c.Query("state") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid state"})
		return
	}
	verifier, _ := c.Cookie("oidc_verifier")
	c.SetCookie("oidc_state", "", -1, "/auth/oidc", "", false, true)
	c.SetCookie("oidc_verifier", "", -1, "/auth/oidc", "", false, true)

	code := c.Query("code")
	if code == "" {
		if errMsg := c.Query("error"); errMsg != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "IdP error: " + errMsg})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing code"})
		return
	}

	claims, err := h.oidc.Exchange(c.Request.Context(), code, verifier)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "OIDC exchange failed"})
		return
	}

	apiKeyID, err := h.upsertSSOUser(c, "oidc", claims.Sub, claims.Email, claims.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to provision SSO user"})
		return
	}

	if err := h.session.Set(c, sso.Claims{APIKeyID: apiKeyID, Email: claims.Email, Provider: "oidc"}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create session"})
		return
	}

	h.audit.Log(c.Request.Context(), audit.Event{
		Actor:    claims.Email,
		Action:   "sso_login",
		Metadata: map[string]any{"provider": "oidc"},
	})
	c.Redirect(http.StatusFound, "/")
}

// ── SAML ─────────────────────────────────────────────────────────────────────

// SAMLMetadata serves the SP metadata XML so the IdP can be configured.
func (h *AuthHandler) SAMLMetadata(c *gin.Context) {
	if h.saml == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "SAML is not configured"})
		return
	}
	xmlBytes, err := h.saml.MetadataXML()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to render metadata"})
		return
	}
	c.Data(http.StatusOK, "application/samlmetadata+xml", xmlBytes)
}

// SAMLLogin redirects the browser to the IdP's SSO endpoint.
func (h *AuthHandler) SAMLLogin(c *gin.Context) {
	if h.saml == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "SAML is not configured"})
		return
	}
	loginURL, err := h.saml.LoginURL()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build SAML request"})
		return
	}
	c.Redirect(http.StatusFound, loginURL)
}

// SAMLACS handles the HTTP-POST binding Assertion Consumer Service callback.
func (h *AuthHandler) SAMLACS(c *gin.Context) {
	if h.saml == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "SAML is not configured"})
		return
	}

	claims, err := h.saml.ParseResponse(c.Request)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid SAML response"})
		return
	}

	apiKeyID, err := h.upsertSSOUser(c, "saml", claims.NameID, claims.Email, claims.Name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to provision SSO user"})
		return
	}

	if err := h.session.Set(c, sso.Claims{APIKeyID: apiKeyID, Email: claims.Email, Provider: "saml"}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create session"})
		return
	}

	h.audit.Log(c.Request.Context(), audit.Event{
		Actor:    claims.Email,
		Action:   "sso_login",
		Metadata: map[string]any{"provider": "saml"},
	})
	c.Redirect(http.StatusFound, "/")
}

// ── Shared ────────────────────────────────────────────────────────────────────

// Me returns the authenticated caller's SSO profile (requires active session).
func (h *AuthHandler) Me(c *gin.Context) {
	claims, err := h.session.Get(c)
	if err != nil || claims == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated via SSO"})
		return
	}

	var user models.SSOUser
	err = h.db.GetContext(c.Request.Context(), &user,
		`SELECT id, email, name, provider, created_at, last_login_at
		   FROM sso_users WHERE api_key_id = $1`,
		claims.APIKeyID,
	)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "SSO user not found"})
		return
	}
	c.JSON(http.StatusOK, user)
}

// Logout clears the session cookie.
func (h *AuthHandler) Logout(c *gin.Context) {
	if h.session != nil {
		h.session.Clear(c)
	}
	c.JSON(http.StatusOK, gin.H{"message": "logged out"})
}

// upsertSSOUser finds or creates an sso_users row and returns its api_key_id
// (as a string). A new API key is created when the user has none.
func (h *AuthHandler) upsertSSOUser(c *gin.Context, provider, externalID, email, name string) (string, error) {
	ctx := c.Request.Context()

	var user models.SSOUser
	err := h.db.GetContext(ctx, &user,
		`SELECT id, email, name, provider, external_id, api_key_id, created_at, last_login_at
		   FROM sso_users WHERE provider = $1 AND external_id = $2`,
		provider, externalID,
	)

	if err != nil {
		// New user — create an API key and insert the row
		keyName := fmt.Sprintf("sso:%s:%s", provider, email)
		_, newKey, err := auth.GenerateKey(keyName, nil)
		if err != nil {
			return "", fmt.Errorf("generate API key: %w", err)
		}
		if _, err := h.db.ExecContext(ctx,
			`INSERT INTO api_keys (id, name, key_hash, prefix, active, created_at)
			 VALUES ($1, $2, $3, $4, true, now())`,
			newKey.ID, newKey.Name, newKey.KeyHash, newKey.Prefix,
		); err != nil {
			return "", fmt.Errorf("insert api_key: %w", err)
		}
		keyID := newKey.ID
		if _, err := h.db.ExecContext(ctx,
			`INSERT INTO sso_users (email, name, provider, external_id, api_key_id)
			 VALUES ($1, $2, $3, $4, $5)`,
			email, name, provider, externalID, keyID,
		); err != nil {
			return "", fmt.Errorf("insert sso_user: %w", err)
		}
		return keyID.String(), nil
	}

	// Existing user — bump last_login_at
	_, _ = h.db.ExecContext(ctx,
		`UPDATE sso_users SET last_login_at = $1, name = $2 WHERE provider = $3 AND external_id = $4`,
		time.Now(), name, provider, externalID,
	)

	// Re-issue API key if the old one was revoked/deleted
	if user.APIKeyID == nil {
		keyName := fmt.Sprintf("sso:%s:%s", provider, email)
		_, reissuedKey, err := auth.GenerateKey(keyName, nil)
		if err != nil {
			return "", err
		}
		if _, err := h.db.ExecContext(ctx,
			`INSERT INTO api_keys (id, name, key_hash, prefix, active, created_at)
			 VALUES ($1, $2, $3, $4, true, now())`,
			reissuedKey.ID, reissuedKey.Name, reissuedKey.KeyHash, reissuedKey.Prefix,
		); err != nil {
			return "", err
		}
		newID := reissuedKey.ID
		_, _ = h.db.ExecContext(ctx,
			`UPDATE sso_users SET api_key_id = $1 WHERE provider = $2 AND external_id = $3`,
			newID, provider, externalID,
		)
		return newID.String(), nil
	}

	return user.APIKeyID.String(), nil
}

// Session returns the SessionManager so the router can pass it to auth middleware.
func (h *AuthHandler) Session() *sso.SessionManager { return h.session }

// Ensure uuid is used
var _ = uuid.New
