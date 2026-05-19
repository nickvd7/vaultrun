package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"

	authpkg "github.com/nickvd7/vaultrun/internal/auth"
)

const actorKey = "actor"

// APIKeyAuth validates Bearer or X-API-Key tokens against the database.
// It also supports a master key (set via MASTER_API_KEY env var) for bootstrapping.
func APIKeyAuth(db *sqlx.DB, masterKey string) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := extractKey(c)
		if key == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing api key"})
			return
		}

		// Master key short-circuit (for initial setup only)
		if masterKey != "" && key == masterKey {
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
