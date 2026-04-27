package middleware

import (
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"

	"github.com/fusion-platform/fusion-bff/internal/session"
)

const CtxKeySession = "fusion-bff/session"

// SessionAuth reads the session cookie and attaches the session to the Gin context.
// Returns 401 if no valid session exists, 403 if requiredPermission is set and missing.
func SessionAuth(store session.Store, cookieName, requiredPermission string) gin.HandlerFunc {
	return func(c *gin.Context) {
		sid, err := c.Cookie(cookieName)
		if err != nil || sid == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
			return
		}
		sess, serr := store.Get(sid)
		if serr != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
			return
		}
		if requiredPermission != "" && !slices.Contains(sess.Permissions, requiredPermission) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		c.Set(CtxKeySession, sess)
		c.Next()
	}
}
