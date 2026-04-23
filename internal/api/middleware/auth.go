package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/fusion-platform/fusion-bff/internal/allowlist"
	"github.com/fusion-platform/fusion-bff/internal/oidc"
	"github.com/fusion-platform/fusion-bff/internal/proxy"
)

// Auth validates the Bearer token, enforces the allowlist, and stores user
// identity in the request context for downstream proxy header injection.
func Auth(validator oidc.TokenValidator, checker allowlist.Checker) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		rawToken := strings.TrimPrefix(header, "Bearer ")

		claims, err := validator.Validate(c.Request.Context(), rawToken)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}

		if !checker.Permitted(claims.Subject, claims.Email) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}

		c.Request = proxy.SetUserContext(c.Request, claims.Subject, claims.Email)
		c.Next()
	}
}
