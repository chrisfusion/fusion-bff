package middleware

import (
	"context"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/oauth2"

	"github.com/fusion-platform/fusion-bff/internal/allowlist"
	"github.com/fusion-platform/fusion-bff/internal/config"
	"github.com/fusion-platform/fusion-bff/internal/oidc"
	"github.com/fusion-platform/fusion-bff/internal/proxy"
	"github.com/fusion-platform/fusion-bff/internal/rbac"
	"github.com/fusion-platform/fusion-bff/internal/session"
)

// APIAuth is the combined session-cookie + Bearer middleware for /api/* routes.
// Cookie auth takes priority: if a valid session cookie is present the user context is
// populated from the stored session (with a silent token refresh if the access token is
// within 30 seconds of expiry). If no valid session cookie is found the middleware falls
// back to the existing Bearer token path, preserving service-to-service auth unchanged.
//
// After authenticating the caller, the middleware checks whether the route requires a
// specific permission (via the RBAC engine). Requests lacking the required permission
// are rejected with 403.
func APIAuth(
	store session.Store,
	refreshFn func(ctx context.Context, refreshToken string) (*oauth2.Token, error),
	validator oidc.TokenValidator,
	checker allowlist.Checker,
	cfg *config.Config,
	engine *rbac.Engine,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		match := rbac.MatchRoute(engine.RoutePermissions(), c.Request.Method, c.Request.URL.Path)

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
				if match.Permission != "" {
					hasGlobal := slices.Contains(sess.Permissions, match.Permission)
					hasScoped := match.ResourceType != "" &&
						hasResourcePerm(sess.ResourcePermissions, match.Permission, match.ResourceType, match.ResourceID)
					if !hasGlobal && !hasScoped {
						c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
						return
					}
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

		if match.Permission != "" {
			_, perms, rerr := engine.Resolve(c.Request.Context(), claims.Subject, claims.Groups)
			if rerr != nil {
				log.Printf("apiauth: rbac resolve for bearer: %v", rerr)
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
				return
			}
			hasGlobal := slices.Contains(perms, match.Permission)
			if !hasGlobal && match.ResourceType != "" {
				resourcePerms, rerr := engine.ResolveResourcePermissions(
					c.Request.Context(), claims.Subject, claims.Groups, perms)
				if rerr != nil {
					log.Printf("apiauth: rbac resolve resource perms for bearer: %v", rerr)
					c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
					return
				}
				if !hasResourcePerm(resourcePerms, match.Permission, match.ResourceType, match.ResourceID) {
					c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
					return
				}
			} else if !hasGlobal {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
				return
			}
		}

		c.Request = proxy.SetUserContext(c.Request, claims.Subject, claims.Email)
		c.Next()
	}
}

func hasResourcePerm(rps []session.ResourcePermission, permission, resourceType, resourceID string) bool {
	for _, rp := range rps {
		if rp.Permission == permission && rp.ResourceType == resourceType && rp.ResourceID == resourceID {
			return true
		}
	}
	return false
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
