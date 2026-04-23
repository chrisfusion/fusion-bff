//go:build e2e

package e2e

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/fusion-platform/fusion-bff/internal/allowlist"
	"github.com/fusion-platform/fusion-bff/internal/api"
	"github.com/fusion-platform/fusion-bff/internal/api/handler"
	"github.com/fusion-platform/fusion-bff/internal/config"
	"github.com/fusion-platform/fusion-bff/internal/oidc"
	"github.com/fusion-platform/fusion-bff/internal/proxy"
	"github.com/fusion-platform/fusion-bff/internal/session"
	"github.com/fusion-platform/fusion-bff/internal/token"
)

const (
	kid      = "test-key-1"
	clientID = "fusion-gui-test"
	sub1     = "user-sub-allowed"
	email1   = "allowed@example.com"
	sub2     = "user-sub-denied"
	email2   = "denied@example.com"
)

var (
	rsaKey       *rsa.PrivateKey
	oidcSrv      *httptest.Server
	bffServer    *httptest.Server
	store        *session.InMemoryStore
	forgeCapture capturedRequest
	indexCapture capturedRequest
	weaveCapture capturedRequest
)

func TestMain(m *testing.M) {
	var err error
	rsaKey, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate RSA key: %v\n", err)
		os.Exit(1)
	}

	oidcSrv = newOIDCServerGlobal(rsaKey, kid, clientID)
	defer oidcSrv.Close()

	forgeSrv := newUpstreamServerGlobal(&forgeCapture)
	defer forgeSrv.Close()

	indexSrv := newUpstreamServerGlobal(&indexCapture)
	defer indexSrv.Close()

	weaveSrv := newUpstreamServerGlobal(&weaveCapture)
	defer weaveSrv.Close()

	saFile, _ := os.CreateTemp("", "sa-token-*")
	saFile.WriteString("test-sa-token")
	saFile.Close()
	defer os.Remove(saFile.Name())

	weaveSAFile, _ := os.CreateTemp("", "weave-sa-token-*")
	weaveSAFile.WriteString("test-weave-sa-token")
	weaveSAFile.Close()
	defer os.Remove(weaveSAFile.Name())

	os.Setenv("OIDC_ISSUER_URL", oidcSrv.URL)
	os.Setenv("OIDC_CLIENT_ID", clientID)
	os.Setenv("OIDC_JWKS_URL", oidcSrv.URL+"/protocol/openid-connect/certs")
	os.Setenv("OIDC_JWKS_CACHE_TTL", "1s")
	os.Setenv("ALLOWED_USERS", sub1+","+email1)
	os.Setenv("FORGE_URL", forgeSrv.URL)
	os.Setenv("INDEX_URL", indexSrv.URL)
	os.Setenv("WEAVE_URL", weaveSrv.URL)
	os.Setenv("K8S_SA_TOKEN_PATH", saFile.Name())
	os.Setenv("WEAVE_SA_TOKEN_PATH", weaveSAFile.Name())
	os.Setenv("SA_TOKEN_CACHE_TTL", "1s")
	os.Setenv("ALLOWLIST_CACHE_TTL", "1s")
	os.Setenv("HTTP_PORT", "0")
	// Session layer — OIDC_REDIRECT_URL is a placeholder; the mock token endpoint ignores it.
	os.Setenv("OIDC_CLIENT_SECRET", "test-client-secret")
	os.Setenv("OIDC_REDIRECT_URL", "http://localhost/bff/callback")
	os.Setenv("OIDC_PUBLIC_AUTH_URL", oidcSrv.URL)
	os.Setenv("SESSION_COOKIE_NAME", "sid")
	os.Setenv("SESSION_COOKIE_DOMAIN", "")
	os.Setenv("SESSION_COOKIE_SECURE", "false")
	os.Setenv("SESSION_MAX_AGE", "8h")
	os.Setenv("SESSION_SECRET", "test-session-secret-at-least-32-bytes!")
	os.Setenv("CORS_ORIGINS", "")

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}

	validator, err := oidc.NewValidator(
		context.Background(),
		cfg.OIDCIssuerURL, cfg.OIDCClientID, cfg.OIDCJWKSURL, cfg.JWKSCacheTTL,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "validator: %v\n", err)
		os.Exit(1)
	}

	checker := allowlist.New(cfg.AllowedUsers)

	store = session.NewInMemoryStore(cfg.SessionMaxAge)

	oauthCfg := &oauth2.Config{
		ClientID:     cfg.OIDCClientID,
		ClientSecret: cfg.OIDCClientSecret,
		Endpoint:     oauth2.Endpoint{TokenURL: cfg.OIDCIssuerURL + "/protocol/openid-connect/token"},
	}
	refreshFn := func(ctx context.Context, rt string) (*oauth2.Token, error) {
		src := oauthCfg.TokenSource(ctx, &oauth2.Token{
			RefreshToken: rt,
			Expiry:       time.Now().Add(-time.Second),
		})
		return src.Token()
	}

	authH := handler.NewAuthHandler(cfg, store, validator, checker)

	saToken := token.NewFileProvider(cfg.SATokenPath, cfg.SATokenCacheTTL)
	weaveSAToken := token.NewFileProvider(cfg.WeaveSATokenPath, cfg.SATokenCacheTTL)

	forgeProxy, _ := proxy.NewUpstreamProxy(cfg.ForgeURL, "/api/forge", saToken)
	indexProxy, _ := proxy.NewUpstreamProxy(cfg.IndexURL, "/api/index", saToken)
	weaveProxy, _ := proxy.NewUpstreamProxy(cfg.WeaveURL, "/api/weave", weaveSAToken)

	router := api.NewRouter(validator, checker, authH, store, refreshFn, cfg, forgeProxy, indexProxy, weaveProxy)
	bffServer = httptest.NewServer(router)
	defer bffServer.Close()

	os.Exit(m.Run())
}

func TestHealthEndpoints(t *testing.T) {
	for _, path := range []string{"/health", "/livez", "/readyz"} {
		resp, err := http.Get(bffServer.URL + path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: want 200, got %d", path, resp.StatusCode)
		}
	}
}

func TestMissingToken(t *testing.T) {
	resp, err := http.Get(bffServer.URL + "/api/forge/v1/environments")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", resp.StatusCode)
	}
}

func TestInvalidToken(t *testing.T) {
	otherKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	tok := mintJWT(t, otherKey, kid, validClaims(oidcSrv.URL, clientID, sub1, email1))
	resp := doRequest(t, "GET", "/api/forge/v1/environments", tok)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", resp.StatusCode)
	}
}

func TestExpiredToken(t *testing.T) {
	claims := validClaims(oidcSrv.URL, clientID, sub1, email1)
	claims.Expiry = time.Now().Add(-1 * time.Minute).Unix()
	tok := mintJWT(t, rsaKey, kid, claims)
	resp := doRequest(t, "GET", "/api/forge/v1/environments", tok)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", resp.StatusCode)
	}
}

func TestWrongAudience(t *testing.T) {
	claims := validClaims(oidcSrv.URL, "wrong-client", sub1, email1)
	tok := mintJWT(t, rsaKey, kid, claims)
	resp := doRequest(t, "GET", "/api/forge/v1/environments", tok)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", resp.StatusCode)
	}
}

func TestForbiddenUser(t *testing.T) {
	tok := mintJWT(t, rsaKey, kid, validClaims(oidcSrv.URL, clientID, sub2, email2))
	resp := doRequest(t, "GET", "/api/forge/v1/environments", tok)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("want 403, got %d", resp.StatusCode)
	}
}

func TestAllowedBySubForward(t *testing.T) {
	forgeCapture.reset()
	tok := mintJWT(t, rsaKey, kid, validClaims(oidcSrv.URL, clientID, sub1, email1))
	resp := doRequest(t, "GET", "/api/forge/v1/environments", tok)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	_, _, headers := forgeCapture.get()
	if got := headers.Get("X-User-ID"); got != sub1 {
		t.Errorf("X-User-ID: want %q, got %q", sub1, got)
	}
	if got := headers.Get("X-User-Email"); got != email1 {
		t.Errorf("X-User-Email: want %q, got %q", email1, got)
	}
	if headers.Get("Authorization") == "Bearer "+tok {
		t.Error("inbound JWT must not be forwarded to upstream")
	}
	if headers.Get("Authorization") == "" {
		t.Error("SA token Authorization header must be set on upstream request")
	}
}

func TestAllowedByEmailForward(t *testing.T) {
	forgeCapture.reset()
	// Use a sub not in the allowlist — access should be granted via email match.
	tok := mintJWT(t, rsaKey, kid, validClaims(oidcSrv.URL, clientID, "other-sub", email1))
	resp := doRequest(t, "GET", "/api/forge/v1/builds", tok)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	_, _, headers := forgeCapture.get()
	if got := headers.Get("X-User-ID"); got != "other-sub" {
		t.Errorf("X-User-ID: want %q, got %q", "other-sub", got)
	}
	if got := headers.Get("X-User-Email"); got != email1 {
		t.Errorf("X-User-Email: want %q, got %q", email1, got)
	}
	if headers.Get("Authorization") == "" {
		t.Error("SA token Authorization header must be set on upstream request")
	}
}

func TestPathStripping(t *testing.T) {
	forgeCapture.reset()
	tok := mintJWT(t, rsaKey, kid, validClaims(oidcSrv.URL, clientID, sub1, email1))
	doRequest(t, "GET", "/api/forge/v1/environments", tok)
	_, path, _ := forgeCapture.get()
	if path != "/v1/environments" {
		t.Errorf("upstream path: want /v1/environments, got %q", path)
	}
}

func TestWeaveForward(t *testing.T) {
	weaveCapture.reset()
	tok := mintJWT(t, rsaKey, kid, validClaims(oidcSrv.URL, clientID, sub1, email1))
	resp := doRequest(t, "GET", "/api/weave/api/v1/chains", tok)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	_, _, headers := weaveCapture.get()
	if got := headers.Get("X-User-ID"); got != sub1 {
		t.Errorf("X-User-ID: want %q, got %q", sub1, got)
	}
	if got := headers.Get("X-User-Email"); got != email1 {
		t.Errorf("X-User-Email: want %q, got %q", email1, got)
	}
	if headers.Get("Authorization") == "Bearer "+tok {
		t.Error("inbound JWT must not be forwarded to weave")
	}
	if headers.Get("Authorization") == "" {
		t.Error("weave SA token Authorization header must be set on upstream request")
	}
}

func TestWeavePathStripping(t *testing.T) {
	weaveCapture.reset()
	tok := mintJWT(t, rsaKey, kid, validClaims(oidcSrv.URL, clientID, sub1, email1))
	doRequest(t, "GET", "/api/weave/api/v1/chains", tok)
	_, path, _ := weaveCapture.get()
	if path != "/api/v1/chains" {
		t.Errorf("upstream path: want /api/v1/chains, got %q", path)
	}
}

func TestWeaveSATokenIsolation(t *testing.T) {
	weaveCapture.reset()
	forgeCapture.reset()
	tok := mintJWT(t, rsaKey, kid, validClaims(oidcSrv.URL, clientID, sub1, email1))

	doRequest(t, "GET", "/api/weave/api/v1/runs", tok)
	doRequest(t, "GET", "/api/forge/v1/venvs", tok)

	_, _, weaveHeaders := weaveCapture.get()
	_, _, forgeHeaders := forgeCapture.get()

	weaveAuth := weaveHeaders.Get("Authorization")
	forgeAuth := forgeHeaders.Get("Authorization")

	if weaveAuth == "" {
		t.Error("weave: SA token Authorization header must be set")
	}
	if forgeAuth == "" {
		t.Error("forge: SA token Authorization header must be set")
	}
	if weaveAuth == forgeAuth {
		t.Error("weave and forge must use separate SA tokens")
	}
}

func doRequest(t *testing.T, method, path, bearerToken string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, bffServer.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}
