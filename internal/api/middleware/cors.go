package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// CORS sets CORS headers for requests from configured origins.
// An empty origins slice disables CORS header injection entirely.
// When origins is non-empty, the exact requesting origin is reflected (never a wildcard)
// and credentials are enabled, which is required for the session cookie to be sent
// on cross-origin API requests from the frontend.
func CORS(origins []string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(origins))
	for _, o := range origins {
		allowed[o] = struct{}{}
	}
	return func(c *gin.Context) {
		if len(allowed) == 0 {
			c.Next()
			return
		}
		origin := c.GetHeader("Origin")
		if _, ok := allowed[origin]; !ok {
			c.Next()
			return
		}
		c.Header("Access-Control-Allow-Origin", origin)
		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Vary", "Origin")
		if c.Request.Method == http.MethodOptions {
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID")
			c.Header("Access-Control-Max-Age", "86400")
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
