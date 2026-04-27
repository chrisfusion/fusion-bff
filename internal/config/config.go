package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	HTTPPort          string
	OIDCIssuerURL     string
	OIDCClientID      string
	OIDCJWKSURL       string
	AllowedUsers      []string
	ForgeURL          string
	IndexURL          string
	WeaveURL          string
	SATokenPath       string
	WeaveSATokenPath  string
	JWKSCacheTTL      time.Duration
	SATokenCacheTTL   time.Duration
	AllowlistCacheTTL time.Duration

	// OAuth2 / BFF session layer
	OIDCClientSecret  string        // OIDC_CLIENT_SECRET — confidential client secret
	OIDCRedirectURL   string        // OIDC_REDIRECT_URL — callback URL registered with the OIDC provider
	OIDCPublicAuthURL string        // OIDC_PUBLIC_AUTH_URL — browser-visible Keycloak URL; defaults to OIDCIssuerURL
	OIDCRevokeURL     string        // derived: {issuer}/protocol/openid-connect/revoke (override: OIDC_REVOKE_URL)
	OIDCEndSessionURL string        // derived: {publicAuthURL}/protocol/openid-connect/logout (override: OIDC_END_SESSION_URL)
	SessionSecret     string        // SESSION_SECRET — required when session layer is enabled
	SessionCookieName string        // SESSION_COOKIE_NAME — default "sid"
	SessionCookieDomain string      // SESSION_COOKIE_DOMAIN — "" = no Domain attr, "auto" = derive from Host
	SessionCookieSecure bool        // SESSION_COOKIE_SECURE — set true in production (HTTPS)
	SessionMaxAge     time.Duration // SESSION_MAX_AGE — default 8h
	CORSOrigins            []string      // CORS_ORIGINS — comma-separated allowed origins
	PostLoginRedirectURL   string        // POST_LOGIN_REDIRECT_URL — where to send the browser after successful login; default "/"

	// Mock OIDC bypass — OIDC_BYPASS=true only; never set in production
	OIDCBypass        bool     // OIDC_BYPASS — enable embedded mock OIDC server
	OIDCBypassBaseURL string   // OIDC_BYPASS_BASE_URL — BFF base URL as seen by the browser (default: http://localhost:{HTTP_PORT})
	OIDCBypassSub     string   // OIDC_BYPASS_SUB — default sub claim pre-filled in the login form
	OIDCBypassEmail   string   // OIDC_BYPASS_EMAIL — default email claim
	OIDCBypassName    string   // OIDC_BYPASS_NAME — default name claim
	OIDCBypassGroups  []string // OIDC_BYPASS_GROUPS — comma-separated groups pre-selected in the login form

	// RBAC
	RBACConfigPath string // RBAC_CONFIG_PATH — path to rbac.yaml; default ./rbac.yaml

	// Database — required when rbac.yaml group_source is "db" or "both"
	DBDSN string // DB_DSN — PostgreSQL connection string
}

func Load() (*Config, error) {
	cfg := &Config{
		HTTPPort:    envOrDefault("HTTP_PORT", "8080"),
		OIDCIssuerURL: os.Getenv("OIDC_ISSUER_URL"),
		OIDCClientID:  os.Getenv("OIDC_CLIENT_ID"),
		OIDCJWKSURL:   os.Getenv("OIDC_JWKS_URL"),
		ForgeURL:         envOrDefault("FORGE_URL", "http://fusion-forge.fusion.svc.cluster.local:8080"),
		IndexURL:         envOrDefault("INDEX_URL", "http://fusion-index-backend.fusion.svc.cluster.local:8080"),
		WeaveURL:         envOrDefault("WEAVE_URL", "http://fusion-weave-api.fusion.svc.cluster.local:8082"),
		SATokenPath:      envOrDefault("K8S_SA_TOKEN_PATH", "/var/run/secrets/kubernetes.io/serviceaccount/token"),
		WeaveSATokenPath: envOrDefault("WEAVE_SA_TOKEN_PATH", "/var/run/secrets/fusion-bff/weave/token"),
	}

	cfg.OIDCBypass = os.Getenv("OIDC_BYPASS") == "true"
	if cfg.OIDCBypass {
		cfg.OIDCBypassBaseURL = envOrDefault("OIDC_BYPASS_BASE_URL", "http://localhost:"+cfg.HTTPPort)
		cfg.OIDCBypassSub     = envOrDefault("OIDC_BYPASS_SUB", "dev-user")
		cfg.OIDCBypassEmail   = envOrDefault("OIDC_BYPASS_EMAIL", "dev@local")
		cfg.OIDCBypassName    = envOrDefault("OIDC_BYPASS_NAME", "Dev User")
		if raw := os.Getenv("OIDC_BYPASS_GROUPS"); raw != "" {
			for _, g := range strings.Split(raw, ",") {
				if g = strings.TrimSpace(g); g != "" {
					cfg.OIDCBypassGroups = append(cfg.OIDCBypassGroups, g)
				}
			}
		}
	}

	cfg.RBACConfigPath = envOrDefault("RBAC_CONFIG_PATH", "./rbac.yaml")
	cfg.DBDSN = os.Getenv("DB_DSN")

	if !cfg.OIDCBypass {
		if cfg.OIDCIssuerURL == "" {
			return nil, fmt.Errorf("OIDC_ISSUER_URL is required")
		}
		if cfg.OIDCClientID == "" {
			return nil, fmt.Errorf("OIDC_CLIENT_ID is required")
		}
	}
	if cfg.OIDCJWKSURL == "" && !cfg.OIDCBypass {
		// Default to Keycloak convention; override with OIDC_JWKS_URL for other providers.
		cfg.OIDCJWKSURL = strings.TrimRight(cfg.OIDCIssuerURL, "/") + "/protocol/openid-connect/certs"
	}

	raw := os.Getenv("ALLOWED_USERS")
	if raw != "" {
		for _, u := range strings.Split(raw, ",") {
			if u = strings.TrimSpace(u); u != "" {
				cfg.AllowedUsers = append(cfg.AllowedUsers, u)
			}
		}
	}

	var err error
	if cfg.JWKSCacheTTL, err = parseDuration("OIDC_JWKS_CACHE_TTL", 15*time.Minute); err != nil {
		return nil, err
	}
	if cfg.SATokenCacheTTL, err = parseDuration("SA_TOKEN_CACHE_TTL", 5*time.Minute); err != nil {
		return nil, err
	}
	if cfg.AllowlistCacheTTL, err = parseDuration("ALLOWLIST_CACHE_TTL", 30*time.Second); err != nil {
		return nil, err
	}
	if cfg.SessionMaxAge, err = parseDuration("SESSION_MAX_AGE", 8*time.Hour); err != nil {
		return nil, err
	}

	// OAuth2 / session layer config.
	cfg.OIDCClientSecret = os.Getenv("OIDC_CLIENT_SECRET")
	cfg.OIDCRedirectURL = os.Getenv("OIDC_REDIRECT_URL")
	cfg.OIDCPublicAuthURL = envOrDefault("OIDC_PUBLIC_AUTH_URL", cfg.OIDCIssuerURL)
	cfg.OIDCRevokeURL = envOrDefault("OIDC_REVOKE_URL",
		strings.TrimRight(cfg.OIDCIssuerURL, "/")+"/protocol/openid-connect/revoke")
	cfg.OIDCEndSessionURL = envOrDefault("OIDC_END_SESSION_URL",
		strings.TrimRight(cfg.OIDCPublicAuthURL, "/")+"/protocol/openid-connect/logout")
	cfg.SessionSecret = os.Getenv("SESSION_SECRET")
	cfg.SessionCookieName = envOrDefault("SESSION_COOKIE_NAME", "sid")
	cfg.SessionCookieDomain = os.Getenv("SESSION_COOKIE_DOMAIN")
	cfg.SessionCookieSecure = os.Getenv("SESSION_COOKIE_SECURE") == "true"
	cfg.PostLoginRedirectURL = envOrDefault("POST_LOGIN_REDIRECT_URL", "/")

	if raw := os.Getenv("CORS_ORIGINS"); raw != "" {
		for _, o := range strings.Split(raw, ",") {
			if o = strings.TrimSpace(o); o != "" {
				cfg.CORSOrigins = append(cfg.CORSOrigins, o)
			}
		}
	}

	// Override OIDC endpoints to point at the embedded mock server.
	// Applied last so env-var values loaded above are replaced unconditionally.
	if cfg.OIDCBypass {
		if cfg.OIDCClientID == "" {
			cfg.OIDCClientID = "fusion-bff-mock"
		}
		internal := "http://localhost:" + cfg.HTTPPort + "/mock-oidc"
		public   := strings.TrimRight(cfg.OIDCBypassBaseURL, "/") + "/mock-oidc"
		cfg.OIDCIssuerURL     = internal
		cfg.OIDCPublicAuthURL = public
		cfg.OIDCRevokeURL     = internal + "/protocol/openid-connect/revoke"
		cfg.OIDCEndSessionURL = public + "/protocol/openid-connect/logout"
		cfg.OIDCClientSecret  = ""
		if cfg.OIDCRedirectURL == "" {
			cfg.OIDCRedirectURL = strings.TrimRight(cfg.OIDCBypassBaseURL, "/") + "/bff/callback"
		}
	}

	return cfg, nil
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseDuration(key string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid duration %q: %w", key, v, err)
	}
	return d, nil
}
