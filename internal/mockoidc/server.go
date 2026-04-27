package mockoidc

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fusion-platform/fusion-bff/internal/config"
	oidcpkg "github.com/fusion-platform/fusion-bff/internal/oidc"
)

// Server is an in-process OIDC provider used only when OIDC_BYPASS=true.
// It generates an RSA keypair at startup, serves a browser login form, issues
// signed JWTs, and exposes a JWKS endpoint — all on the same Gin engine as the BFF.
type Server struct {
	privateKey      *rsa.PrivateKey
	keyID           string
	issuer          string   // cluster-internal base URL (used as iss claim in JWTs)
	clientID        string
	defaultSub      string
	defaultEmail    string
	defaultName     string
	defaultGroups   []string // pre-selected groups from OIDC_BYPASS_GROUPS
	availableGroups []string // all group names from rbac.yaml for the form selector
	jwks            []byte   // pre-serialised JWKS JSON

	mu        sync.Mutex
	codes     map[string]*codeEntry  // auth code → identity (5-min TTL)
	refreshes map[string]*identity   // refresh token → identity (no expiry; dev-only)
}

type identity struct {
	sub, email, name string
	groups           []string
}

type codeEntry struct {
	ident     identity
	expiresAt time.Time
}

// New builds a Server from the bypass configuration. It generates a fresh RSA
// keypair each time the BFF starts, so tokens issued in one run are invalid after restart.
// availableGroups is the list of group names from rbac.yaml for the login form selector.
func New(cfg *config.Config, availableGroups []string) *Server {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatalf("mockoidc: generate RSA key: %v", err)
	}
	s := &Server{
		privateKey:      key,
		keyID:           "mock-1",
		issuer:          "http://localhost:" + cfg.HTTPPort + "/mock-oidc",
		clientID:        cfg.OIDCClientID,
		defaultSub:      cfg.OIDCBypassSub,
		defaultEmail:    cfg.OIDCBypassEmail,
		defaultName:     cfg.OIDCBypassName,
		defaultGroups:   cfg.OIDCBypassGroups,
		availableGroups: availableGroups,
		codes:           make(map[string]*codeEntry),
		refreshes:       make(map[string]*identity),
	}
	s.jwks = s.buildJWKS()
	return s
}

// Validator returns a TokenValidator that verifies JWTs using the in-memory key.
// Use this instead of oidc.NewValidator when bypass mode is active.
func (s *Server) Validator() oidcpkg.TokenValidator {
	return &mockValidator{publicKey: &s.privateKey.PublicKey}
}

// RegisterRoutes mounts the mock OIDC endpoints on the provided engine.
// Routes mirror Keycloak's path conventions so the existing AuthHandler needs no changes.
func (s *Server) RegisterRoutes(r *gin.Engine) {
	g := r.Group("/mock-oidc/protocol/openid-connect")
	g.GET("/auth", s.handleAuthForm)
	g.POST("/auth", s.handleAuthSubmit)
	g.POST("/token", s.handleToken)
	g.GET("/certs", s.handleCerts)
	g.POST("/revoke", func(c *gin.Context) { c.Status(http.StatusOK) })
	g.GET("/logout", s.handleLogout)
}

// buildJWKS serialises the RSA public key as a JWK Set.
func (s *Server) buildJWKS() []byte {
	pub := &s.privateKey.PublicKey
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes())
	jwks := map[string]interface{}{
		"keys": []map[string]string{
			{"kty": "RSA", "kid": s.keyID, "alg": "RS256", "use": "sig", "n": n, "e": e},
		},
	}
	b, _ := json.Marshal(jwks)
	return b
}

var loginTmpl = template.Must(template.New("login").Parse(`<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>Mock OIDC — Dev Login</title>
<style>
body{font-family:sans-serif;max-width:440px;margin:60px auto;padding:0 16px}
.warn{background:#fff3cd;border:1px solid #ffc107;padding:10px 14px;border-radius:4px;margin-bottom:24px;font-size:14px}
h2{margin-bottom:6px}
label{display:block;margin-bottom:14px;font-size:14px;color:#333}
input,select{display:block;width:100%;box-sizing:border-box;padding:7px 9px;margin-top:4px;border:1px solid #bbb;border-radius:3px;font-size:14px}
select[multiple]{height:auto;min-height:80px}
button{padding:9px 28px;background:#1a73e8;color:#fff;border:none;border-radius:3px;cursor:pointer;font-size:15px}
small{color:#666;font-size:12px;display:block;margin-top:2px}
</style>
</head>
<body>
<h2>Mock OIDC Login</h2>
<div class="warn">⚠️ <strong>Development mode — not for production use.</strong></div>
<form method="POST">
<input type="hidden" name="state" value="{{.State}}">
<input type="hidden" name="redirect_uri" value="{{.RedirectURI}}">
<label>Subject (sub)
  <input name="sub" value="{{.Sub}}">
</label>
<label>Email
  <input name="email" type="email" value="{{.Email}}">
</label>
<label>Display name
  <input name="name" value="{{.Name}}">
</label>
{{if .Groups}}
<label>Groups
  <small>Hold Ctrl/Cmd to select multiple</small>
  <select name="groups" multiple>
  {{range .Groups}}<option value="{{.Name}}"{{if .Selected}} selected{{end}}>{{.Name}}</option>
  {{end}}
  </select>
</label>
{{end}}
<button type="submit">Login</button>
</form>
</body>
</html>`))

type groupOption struct {
	Name     string
	Selected bool
}

// handleAuthForm renders the login form. The browser is redirected here by AuthHandler.Login.
func (s *Server) handleAuthForm(c *gin.Context) {
	defaultSet := make(map[string]bool, len(s.defaultGroups))
	for _, g := range s.defaultGroups {
		defaultSet[g] = true
	}
	groups := make([]groupOption, 0, len(s.availableGroups))
	for _, g := range s.availableGroups {
		groups = append(groups, groupOption{Name: g, Selected: defaultSet[g]})
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	_ = loginTmpl.Execute(c.Writer, map[string]interface{}{
		"State":       c.Query("state"),
		"RedirectURI": c.Query("redirect_uri"),
		"Sub":         s.defaultSub,
		"Email":       s.defaultEmail,
		"Name":        s.defaultName,
		"Groups":      groups,
	})
}

// handleAuthSubmit processes the login form, generates an auth code, and redirects
// back to the BFF callback with code and state.
func (s *Server) handleAuthSubmit(c *gin.Context) {
	state := c.PostForm("state")
	redirectURI := c.PostForm("redirect_uri")
	if redirectURI == "" {
		// Fallback: redirect to the BFF callback on the same host.
		redirectURI = "http://" + c.Request.Host + "/bff/callback"
	}

	selectedGroups := c.PostFormArray("groups")

	ident := identity{
		sub:    strings.TrimSpace(c.PostForm("sub")),
		email:  strings.TrimSpace(c.PostForm("email")),
		name:   strings.TrimSpace(c.PostForm("name")),
		groups: selectedGroups,
	}
	if ident.sub == "" {
		ident.sub = s.defaultSub
	}

	code, err := randomHex(16)
	if err != nil {
		c.String(http.StatusInternalServerError, "internal error")
		return
	}

	s.mu.Lock()
	s.codes[code] = &codeEntry{ident: ident, expiresAt: time.Now().Add(5 * time.Minute)}
	s.mu.Unlock()

	target := redirectURI + "?code=" + url.QueryEscape(code) + "&state=" + url.QueryEscape(state)
	c.Redirect(http.StatusFound, target)
}

// handleToken exchanges an auth code or refresh token for a signed JWT pair.
func (s *Server) handleToken(c *gin.Context) {
	var ident identity

	switch c.PostForm("grant_type") {
	case "authorization_code":
		code := c.PostForm("code")
		s.mu.Lock()
		entry := s.codes[code]
		delete(s.codes, code)
		s.mu.Unlock()
		if entry == nil || time.Now().After(entry.expiresAt) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_grant"})
			return
		}
		ident = entry.ident

	case "refresh_token":
		rt := c.PostForm("refresh_token")
		s.mu.Lock()
		id := s.refreshes[rt]
		s.mu.Unlock()
		if id != nil {
			ident = *id
		} else {
			ident = identity{sub: s.defaultSub, email: s.defaultEmail, name: s.defaultName, groups: s.defaultGroups}
		}

	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported_grant_type"})
		return
	}

	now := time.Now()
	idToken, err := s.mintJWT(ident, now)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "server_error"})
		return
	}
	accessToken, err := s.mintJWT(ident, now)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "server_error"})
		return
	}

	rt, err := randomHex(16)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "server_error"})
		return
	}
	s.mu.Lock()
	s.refreshes[rt] = &ident
	s.mu.Unlock()

	c.JSON(http.StatusOK, gin.H{
		"access_token":  accessToken,
		"id_token":      idToken,
		"refresh_token": rt,
		"token_type":    "bearer",
		"expires_in":    86400,
	})
}

// handleCerts serves the JWKS so that the mockValidator can be compared against it
// if needed, and for compatibility with any client that fetches keys directly.
func (s *Server) handleCerts(c *gin.Context) {
	c.Data(http.StatusOK, "application/json", s.jwks)
}

// handleLogout redirects to the post_logout_redirect_uri or "/" when the browser
// is sent here by AuthHandler.Logout.
func (s *Server) handleLogout(c *gin.Context) {
	redirect := c.Query("post_logout_redirect_uri")
	if redirect == "" {
		redirect = "/"
	}
	c.Redirect(http.StatusFound, redirect)
}

// mintJWT issues a signed RS256 JWT with the standard OIDC claims.
// Tokens are valid for 24 hours, which avoids silent refresh during normal dev sessions.
func (s *Server) mintJWT(id identity, now time.Time) (string, error) {
	headerJSON, _ := json.Marshal(map[string]string{
		"alg": "RS256",
		"kid": s.keyID,
		"typ": "JWT",
	})
	groups := id.groups
	if groups == nil {
		groups = []string{}
	}
	payloadJSON, err := json.Marshal(map[string]interface{}{
		"iss":    s.issuer,
		"aud":    []string{s.clientID},
		"sub":    id.sub,
		"email":  id.email,
		"name":   id.name,
		"groups": groups,
		"iat":    now.Unix(),
		"exp":    now.Add(24 * time.Hour).Unix(),
	})
	if err != nil {
		return "", fmt.Errorf("marshal claims: %w", err)
	}

	hdr := base64.RawURLEncoding.EncodeToString(headerJSON)
	pay := base64.RawURLEncoding.EncodeToString(payloadJSON)
	sigInput := hdr + "." + pay

	h := sha256.Sum256([]byte(sigInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, s.privateKey, crypto.SHA256, h[:])
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}

	return sigInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return hex.EncodeToString(b), nil
}
