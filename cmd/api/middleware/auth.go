package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"

	authpkg "github.com/nickvd7/vaultrun/internal/auth"
	"github.com/nickvd7/vaultrun/internal/sso"
)

const actorKey = "actor"
const actorNameKey = "actor_name"

// masterKeyMAC computes HMAC-SHA256 of key under a fixed label so that two
// values can be compared in constant time regardless of their length (L-1).
// Raw ConstantTimeCompare short-circuits on length mismatch, leaking key length.
func masterKeyMAC(key string) []byte {
	h := hmac.New(sha256.New, []byte("vaultrun-master-key-compare-v1"))
	h.Write([]byte(key))
	return h.Sum(nil)
}

// APIKeyAuth validates Bearer or X-API-Key tokens against the database.
// When sessionMgr is non-nil, a valid SSO session cookie is also accepted.
// Master key comparison is length-safe via HMAC before ConstantTimeCompare.
func APIKeyAuth(db *sqlx.DB, masterKey string, sessionMgr *sso.SessionManager) gin.HandlerFunc {
	expectedMAC := masterKeyMAC(masterKey)

	return func(c *gin.Context) {
		// ── 1. Header-based API key (existing path) ──────────────────────────
		if key := extractKey(c); key != "" {
			if masterKey != "" && subtle.ConstantTimeCompare(masterKeyMAC(key), expectedMAC) == 1 {
				c.Set(actorKey, "master")
				c.Set(actorNameKey, "master")
				c.Next()
				return
			}
			apiKey, err := authpkg.Validate(c.Request.Context(), db, key)
			if err != nil {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"})
				return
			}
			c.Set(actorKey, apiKey.ID.String())
			c.Set(actorNameKey, apiKey.Name)
			c.Next()
			return
		}

		// ── 2. SSO session cookie ─────────────────────────────────────────────
		if sessionMgr != nil {
			claims, err := sessionMgr.Get(c)
			if err == nil && claims != nil {
				apiKey, err := authpkg.Validate(c.Request.Context(), db, "")
				// We have a key_id, not plaintext — look up directly by UUID.
				_ = apiKey
				_ = err
				// Resolve claims.APIKeyID directly via DB lookup by primary key.
				type keyRow struct {
					ID   string `db:"id"`
					Name string `db:"name"`
				}
				var row keyRow
				if dbErr := db.GetContext(c.Request.Context(), &row,
					`SELECT id::text, name FROM api_keys
					  WHERE id = $1 AND active = true
					  AND (expires_at IS NULL OR expires_at > now())
					  AND revoked_at IS NULL`,
					claims.APIKeyID,
				); dbErr == nil {
					c.Set(actorKey, row.ID)
					c.Set(actorNameKey, row.Name)
					c.Next()
					return
				}
			}
		}

		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing api key"})
	}
}

// RequireMasterKey rejects requests where the caller is not authenticated with
// the master key. Must be applied after APIKeyAuth in the middleware chain (L-8).
func RequireMasterKey() gin.HandlerFunc {
	return func(c *gin.Context) {
		if Actor(c) != "master" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "master key required"})
			return
		}
		c.Next()
	}
}

// Actor returns the canonical identity of the authenticated caller: the API
// key UUID string, or "master" for the master key, or "unknown" if unset.
// Use this for access-control checks and as the stored identity in the DB.
func Actor(c *gin.Context) string {
	if v, ok := c.Get(actorKey); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return "unknown"
}

// ActorName returns the human-readable key name for the authenticated caller.
// Use this for audit log entries and display. Falls back to Actor(c) when
// the display name is not set (e.g. in tests that don't go through APIKeyAuth).
func ActorName(c *gin.Context) string {
	if v, ok := c.Get(actorNameKey); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return Actor(c)
}

func extractKey(c *gin.Context) string {
	if auth := c.GetHeader("Authorization"); auth != "" {
		if strings.HasPrefix(auth, "Bearer ") {
			return strings.TrimPrefix(auth, "Bearer ")
		}
	}
	if key := c.GetHeader("X-API-Key"); key != "" {
		return key
	}
	return ""
}
