package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/oauth2"

	"github.com/fusion-platform/fusion-bff/internal/allowlist"
	"github.com/fusion-platform/fusion-bff/internal/config"
	"github.com/fusion-platform/fusion-bff/internal/oidc"
	"github.com/fusion-platform/fusion-bff/internal/rbac"
	"github.com/fusion-platform/fusion-bff/internal/session"
)

// AuthHandler implements the four BFF session endpoints.
type AuthHandler struct {
	oauth2Cfg     *oauth2.Config
	revokeURL     string // token revocation endpoint (cluster-internal)
	endSessionURL string // Keycloak end_session endpoint (browser-visible public URL)
	store         session.Store
	validator     oidc.TokenValidator
	checker       allowlist.Checker
	engine        *rbac.Engine
	cookieName           string
	cookieDomain         string // "" = no Domain attr, "auto" = derive from Host header
	cookieSecure         bool
	cookieMaxAge         int // seconds
	postLoginRedirectURL string
}

// NewAuthHandler constructs an AuthHandler from the service config.
// The oauth2.Config is assembled here to keep the split between the public auth URL
// (for browser redirects) and the cluster-internal token URL (for server-side calls).
func NewAuthHandler(
	cfg *config.Config,
	store session.Store,
	validator oidc.TokenValidator,
	checker allowlist.Checker,
	engine *rbac.Engine,
) *AuthHandler {
	return &AuthHandler{
		oauth2Cfg: &oauth2.Config{
			ClientID:     cfg.OIDCClientID,
			ClientSecret: cfg.OIDCClientSecret,
			RedirectURL:  cfg.OIDCRedirectURL,
			Scopes:       []string{"openid", "email", "profile", "offline_access"},
			Endpoint: oauth2.Endpoint{
				// AuthURL uses the browser-visible public URL for the redirect.
				AuthURL: strings.TrimRight(cfg.OIDCPublicAuthURL, "/") +
					"/protocol/openid-connect/auth",
				// TokenURL uses the cluster-internal issuer URL for server-to-server calls.
				TokenURL: strings.TrimRight(cfg.OIDCIssuerURL, "/") +
					"/protocol/openid-connect/token",
			},
		},
		revokeURL:    cfg.OIDCRevokeURL,
		endSessionURL: cfg.OIDCEndSessionURL,
		store:         store,
		validator:     validator,
		checker:       checker,
		engine:        engine,
		cookieName:           cfg.SessionCookieName,
		cookieDomain:         cfg.SessionCookieDomain,
		cookieSecure:         cfg.SessionCookieSecure,
		cookieMaxAge:         int(cfg.SessionMaxAge.Seconds()),
		postLoginRedirectURL: cfg.PostLoginRedirectURL,
	}
}

// Login generates PKCE parameters, stores the pending state, and redirects the
// browser to the OIDC provider's authorization endpoint.
func (h *AuthHandler) Login(c *gin.Context) {
	state, err := generateRandomHex(16)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	verifier := oauth2.GenerateVerifier()
	h.store.SavePending(state, verifier)
	authURL := h.oauth2Cfg.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))
	c.Redirect(http.StatusFound, authURL)
}

// Callback handles the redirect from the OIDC provider, exchanges the authorization
// code for tokens, validates the ID token, creates a server-side session, and sets
// the HttpOnly session cookie.
func (h *AuthHandler) Callback(c *gin.Context) {
	state := c.Query("state")
	code := c.Query("code")
	if state == "" || code == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "missing state or code"})
		return
	}

	verifier, ok := h.store.TakePending(state)
	if !ok {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid or expired state"})
		return
	}

	tok, err := h.oauth2Cfg.Exchange(c.Request.Context(), code, oauth2.VerifierOption(verifier))
	if err != nil {
		log.Printf("callback: token exchange: %v", err)
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": "token exchange failed"})
		return
	}

	rawIDToken, _ := tok.Extra("id_token").(string)
	if rawIDToken == "" {
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": "no id_token in response"})
		return
	}

	claims, err := h.validator.Validate(c.Request.Context(), rawIDToken)
	if err != nil {
		log.Printf("callback: id_token validation: %v", err)
		c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": "id_token validation failed"})
		return
	}

	if !h.checker.Permitted(claims.Subject, claims.Email) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	roles, permissions, err := h.engine.Resolve(c.Request.Context(), claims.Subject, claims.Groups)
	if err != nil {
		log.Printf("callback: rbac resolve: %v", err)
		// Non-fatal: user gets no roles/permissions but can still log in.
		roles, permissions = nil, nil
	}

	resourcePerms, err := h.engine.ResolveResourcePermissions(c.Request.Context(), claims.Subject, claims.Groups, roles)
	if err != nil {
		log.Printf("callback: rbac resolve resource perms: %v", err)
		resourcePerms = nil
	}

	sess := &session.Session{
		Sub:                 claims.Subject,
		Email:               claims.Email,
		Name:                claims.Name,
		Roles:               roles,
		Permissions:         permissions,
		ResourcePermissions: resourcePerms,
		AccessToken:         tok.AccessToken,
		RefreshToken:        tok.RefreshToken,
		IDToken:             rawIDToken,
		ExpiresAt:           tok.Expiry,
	}

	sid, err := h.store.Create(sess)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	h.setSessionCookie(c, sid)
	c.Redirect(http.StatusFound, h.postLoginRedirectURL)
}

// Logout revokes the refresh token at the OIDC provider, deletes the server-side
// session, clears the cookie, and redirects to the provider's end_session endpoint.
func (h *AuthHandler) Logout(c *gin.Context) {
	sid, err := c.Cookie(h.cookieName)
	if err != nil || sid == "" {
		c.Redirect(http.StatusFound, h.endSessionURL)
		return
	}

	sess, serr := h.store.Get(sid)
	h.store.Delete(sid)
	h.clearSessionCookie(c)

	if serr == nil && sess.RefreshToken != "" {
		h.revokeRefreshToken(sess.RefreshToken)
	}

	endURL := h.endSessionURL
	if serr == nil && sess.IDToken != "" {
		endURL += "?post_logout_redirect_uri=" + url.QueryEscape("/") +
			"&id_token_hint=" + url.QueryEscape(sess.IDToken) +
			"&client_id=" + url.QueryEscape(h.oauth2Cfg.ClientID)
	}
	c.Redirect(http.StatusFound, endURL)
}

// UserInfo returns the authenticated user's identity from the session.
// Returns 401 if no valid session cookie is present.
func (h *AuthHandler) UserInfo(c *gin.Context) {
	sid, err := c.Cookie(h.cookieName)
	if err != nil || sid == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}
	sess, serr := h.store.Get(sid)
	if serr != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}
	roles := sess.Roles
	if roles == nil {
		roles = []string{}
	}
	permissions := sess.Permissions
	if permissions == nil {
		permissions = []string{}
	}
	resourcePerms := sess.ResourcePermissions
	if resourcePerms == nil {
		resourcePerms = []session.ResourcePermission{}
	}
	c.JSON(http.StatusOK, gin.H{
		"sub":                  sess.Sub,
		"email":                sess.Email,
		"name":                 sess.Name,
		"roles":                roles,
		"permissions":          permissions,
		"resource_permissions": resourcePerms,
	})
}

// setSessionCookie writes the session cookie with the configured options.
func (h *AuthHandler) setSessionCookie(c *gin.Context, sid string) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     h.cookieName,
		Value:    sid,
		Path:     "/",
		Domain:   session.CookieDomain(h.cookieDomain, c.Request.Host),
		MaxAge:   h.cookieMaxAge,
		HttpOnly: true,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

// clearSessionCookie instructs the browser to delete the session cookie.
// The Domain attribute must match the original set-cookie call.
func (h *AuthHandler) clearSessionCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     h.cookieName,
		Value:    "",
		Path:     "/",
		Domain:   session.CookieDomain(h.cookieDomain, c.Request.Host),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

// revokeRefreshToken calls the OIDC revocation endpoint on a best-effort basis.
// Failures are logged but do not prevent the logout from completing.
func (h *AuthHandler) revokeRefreshToken(refreshToken string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	data := url.Values{
		"token":           {refreshToken},
		"token_type_hint": {"refresh_token"},
		"client_id":       {h.oauth2Cfg.ClientID},
		"client_secret":   {h.oauth2Cfg.ClientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.revokeURL,
		strings.NewReader(data.Encode()))
	if err != nil {
		log.Printf("revoke: build request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("revoke: %v", err)
		return
	}
	resp.Body.Close()
}

// resolvedCookieDomain computes the Domain attribute value from the configured setting.
// "auto" derives the shared parent domain from the request Host so that subdomains
// (e.g. spectra.fusion.local and bff.fusion.local) can share the cookie.
// On localhost or single-label hosts, the domain attribute is omitted.
func resolvedCookieDomain(configured, requestHost string) string {
	if configured != "auto" {
		return configured
	}
	host := requestHost
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	if host == "localhost" || !strings.Contains(host, ".") {
		return ""
	}
	return "." + host[strings.Index(host, ".")+1:]
}

// generateRandomHex returns a cryptographically random hex string of n bytes.
func generateRandomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
