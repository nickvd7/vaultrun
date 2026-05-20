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
)

const actorKey = "actor"

// masterKeyMAC computes HMAC-SHA256 of key under a fixed label so that two
// values can be compared in constant time regardless of their length (L-1).
// Raw ConstantTimeCompare short-circuits on length mismatch, leaking key length.
func masterKeyMAC(key string) []byte {
	h := hmac.New(sha256.New, []byte("vaultrun-master-key-compare-v1"))
	h.Write([]byte(key))
	return h.Sum(nil)
}

// APIKeyAuth validates Bearer or X-API-Key tokens against the database.
// Master key comparison is length-safe via HMAC before ConstantTimeCompare.
func APIKeyAuth(db *sqlx.DB, masterKey string) gin.HandlerFunc {
	// Pre-compute once at startup.
	expectedMAC := masterKeyMAC(masterKey)

	return func(c *gin.Context) {
		key := extractKey(c)
		if key == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing api key"})
			return
		}

		// Master key check: HMAC both sides to a fixed-length value so the
		// comparison is constant-time regardless of candidate length (L-1).
		if masterKey != "" && subtle.ConstantTimeCompare(masterKeyMAC(key), expectedMAC) == 1 {
			c.Set(actorKey, "master")
			c.Next()
			return
		}

		apiKey, err := authpkg.Validate(c.Request.Context(), db, key)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"})
			return
		}

		c.Set(actorKey, apiKey.Name)
		c.Next()
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

func Actor(c *gin.Context) string {
	if v, ok := c.Get(actorKey); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return "unknown"
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
