package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"

	authpkg "github.com/nickvd7/vaultrun/internal/auth"
)

const actorKey = "actor"

// APIKeyAuth validates Bearer or X-API-Key tokens against the database.
// The master key path uses constant-time comparison to prevent timing attacks.
func APIKeyAuth(db *sqlx.DB, masterKey string) gin.HandlerFunc {
	masterKeyBytes := []byte(masterKey)
	return func(c *gin.Context) {
		key := extractKey(c)
		if key == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing api key"})
			return
		}

		// Master key short-circuit (for initial setup only).
		// Use constant-time compare to prevent timing-based brute-force.
		if masterKey != "" && subtle.ConstantTimeCompare([]byte(key), masterKeyBytes) == 1 {
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
