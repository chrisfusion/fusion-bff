package middleware

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/oauth2"

	"github.com/fusion-platform/fusion-bff/internal/allowlist"
	"github.com/fusion-platform/fusion-bff/internal/config"
	"github.com/fusion-platform/fusion-bff/internal/oidc"
	"github.com/fusion-platform/fusion-bff/internal/proxy"
	"github.com/fusion-platform/fusion-bff/internal/session"
)

// APIAuth is the combined session-cookie + Bearer middleware for /api/* routes.
// Cookie auth takes priority: if a valid session cookie is present the user context is
// populated from the stored session (with a silent token refresh if the access token is
// within 30 seconds of expiry). If no valid session cookie is found the middleware falls
// back to the existing Bearer token path, preserving service-to-service auth unchanged.
func APIAuth(
	store session.Store,
	refreshFn func(ctx context.Context, refreshToken string) (*oauth2.Token, error),
	validator oidc.TokenValidator,
	checker allowlist.Checker,
	cfg *config.Config,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		if sid, err := c.Cookie(cfg.SessionCookieName); err == nil && sid != "" {
			if sess, serr := store.Get(sid); serr == nil {
				// Silently refresh when the access token is within 30 seconds of expiry.
				if time.Until(sess.ExpiresAt) < 30*time.Second {
					newTok, rerr := refreshFn(c.Request.Context(), sess.RefreshToken)
					if rerr != nil {
						store.Delete(sid)
						clearSessionCookie(c, cfg)
						c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
						return
					}
					sess.AccessToken = newTok.AccessToken
					if newTok.RefreshToken != "" {
						// Store the rotated refresh token if the provider issued one.
						sess.RefreshToken = newTok.RefreshToken
					}
					sess.ExpiresAt = newTok.Expiry
					// Ignore update error — if the session was concurrently deleted,
					// the next request will re-authenticate through the Bearer fallback.
					_ = store.Update(sess)
				}
				c.Request = proxy.SetUserContext(c.Request, sess.Sub, sess.Email)
				c.Next()
				return
			}
		}

		// Bearer fallback — for service-to-service calls and CLI clients.
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

// clearSessionCookie overwrites the session cookie with an expired one to instruct
// the browser to delete it. The Domain attribute must match the original set-cookie
// call so the browser recognises it as the same cookie.
func clearSessionCookie(c *gin.Context, cfg *config.Config) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cfg.SessionCookieName,
		Value:    "",
		Path:     "/",
		Domain:   session.CookieDomain(cfg.SessionCookieDomain, c.Request.Host),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   cfg.SessionCookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}
