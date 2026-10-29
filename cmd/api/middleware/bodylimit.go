package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// MaxJSONBody caps the body of every request that is not a file upload. The
// largest legitimate JSON bodies (verify observations, mission definitions)
// are well under a megabyte.
const MaxJSONBody = 10 << 20

// BodyLimit stops a request body from being read past a limit: maxUpload for
// multipart file uploads (the largest allowed file plus room for the form),
// MaxJSONBody for everything else. Without it the JSON binder reads any body
// into memory and the multipart parser spools any upload to disk before a
// handler can check its size, so a few large requests could exhaust the API's
// memory or disk.
func BodyLimit(maxUpload int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil && c.Request.Body != http.NoBody {
			limit := int64(MaxJSONBody)
			if strings.HasPrefix(c.ContentType(), "multipart/") {
				limit = maxUpload
			}
			if c.Request.ContentLength > limit {
				c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body too large"})
				return
			}
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		}
		c.Next()
	}
}
