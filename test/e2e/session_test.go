//go:build e2e

package e2e

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fusion-platform/fusion-bff/internal/session"
)

// clientWithCookies returns an http.Client that stores cookies but does NOT follow
// redirects — tests inspect redirect Location headers directly.
func clientWithCookies(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &http.Client{
		Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse // stop after first redirect
		},
	}
}

// doSessionRequest performs a request using the provided cookie-aware client.
func doSessionRequest(t *testing.T, client *http.Client, method, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, bffServer.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// sessionCookie extracts the "sid" cookie value from a response.
func sessionCookie(resp *http.Response) string {
	for _, c := range resp.Cookies() {
		if c.Name == "sid" {
			return c.Value
		}
	}
	return ""
}

// TestLoginRedirect verifies that GET /bff/login redirects to the OIDC authorization endpoint
// and includes the required PKCE and state parameters.
func TestLoginRedirect(t *testing.T) {
	client := clientWithCookies(t)
	resp := doSessionRequest(t, client, http.MethodGet, "/bff/login")

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("want 302, got %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc == "" {
		t.Fatal("expected Location header")
	}
	if !strings.Contains(loc, "code_challenge=") {
		t.Errorf("Location should contain code_challenge; got %q", loc)
	}
	if !strings.Contains(loc, "code_challenge_method=S256") {
		t.Errorf("Location should contain code_challenge_method=S256; got %q", loc)
	}
	state := parseQueryParam(loc, "state")
	if state == "" {
		t.Error("Location should contain state parameter")
	}
}

// TestCallbackFlow exercises the full authorization code exchange:
//  1. GET /bff/login → extract state from redirect
//  2. GET /bff/callback?code=testcode&state=<state> → expect sid cookie + redirect to /
func TestCallbackFlow(t *testing.T) {
	client := clientWithCookies(t)

	// Step 1: get the state from the login redirect.
	loginResp := doSessionRequest(t, client, http.MethodGet, "/bff/login")
	if loginResp.StatusCode != http.StatusFound {
		t.Fatalf("login: want 302, got %d", loginResp.StatusCode)
	}
	state := parseQueryParam(loginResp.Header.Get("Location"), "state")
	if state == "" {
		t.Fatal("no state in login redirect")
	}

	// Step 2: simulate the OIDC provider redirecting back with an auth code.
	callbackURL := "/bff/callback?code=test-auth-code&state=" + url.QueryEscape(state)
	cbResp := doSessionRequest(t, client, http.MethodGet, callbackURL)

	if cbResp.StatusCode != http.StatusFound {
		t.Fatalf("callback: want 302, got %d", cbResp.StatusCode)
	}
	if cbResp.Header.Get("Location") != "/" {
		t.Errorf("callback: want redirect to /, got %q", cbResp.Header.Get("Location"))
	}
	if sid := sessionCookie(cbResp); sid == "" {
		t.Error("callback: expected sid cookie in response")
	}
}

// TestUserInfoAuthenticated verifies that GET /bff/userinfo returns user identity
// for a client that has completed the login flow.
func TestUserInfoAuthenticated(t *testing.T) {
	sid := loginAndGetSID(t)

	req, _ := http.NewRequest(http.MethodGet, bffServer.URL+"/bff/userinfo", nil)
	req.AddCookie(&http.Cookie{Name: "sid", Value: sid})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("userinfo: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("userinfo: want 200, got %d", resp.StatusCode)
	}
}

// TestUserInfoUnauthenticated verifies that GET /bff/userinfo returns 401 without a cookie.
func TestUserInfoUnauthenticated(t *testing.T) {
	resp, err := http.Get(bffServer.URL + "/bff/userinfo")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", resp.StatusCode)
	}
}

// TestAPIWithSessionCookie verifies that /api/* requests authenticated via the session
// cookie are proxied to the upstream with correct X-User-ID and X-User-Email headers.
func TestAPIWithSessionCookie(t *testing.T) {
	forgeCapture.reset()
	sid := loginAndGetSID(t)

	req, _ := http.NewRequest(http.MethodGet, bffServer.URL+"/api/forge/v1/environments", nil)
	req.AddCookie(&http.Cookie{Name: "sid", Value: sid})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("api with cookie: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("api with cookie: want 200, got %d", resp.StatusCode)
	}
	_, _, headers := forgeCapture.get()
	if got := headers.Get("X-User-ID"); got != sub1 {
		t.Errorf("X-User-ID: want %q, got %q", sub1, got)
	}
	if got := headers.Get("X-User-Email"); got != email1 {
		t.Errorf("X-User-Email: want %q, got %q", email1, got)
	}
	if headers.Get("Authorization") == "" {
		t.Error("SA token Authorization header must be set on upstream request")
	}
}

// TestAPIBearerStillWorks verifies that the Bearer token path is unaffected by the
// session layer — existing service-to-service calls continue to work.
func TestAPIBearerStillWorks(t *testing.T) {
	forgeCapture.reset()
	tok := mintJWT(t, rsaKey, kid, validClaims(oidcSrv.URL, clientID, sub1, email1))
	resp := doRequest(t, "GET", "/api/forge/v1/environments", tok)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bearer: want 200, got %d", resp.StatusCode)
	}
	_, _, headers := forgeCapture.get()
	if got := headers.Get("X-User-ID"); got != sub1 {
		t.Errorf("X-User-ID: want %q, got %q", sub1, got)
	}
}

// TestLogout verifies that POST /bff/logout deletes the session, clears the cookie,
// calls the token revocation endpoint, and redirects to the end_session URL.
func TestLogout(t *testing.T) {
	sid := loginAndGetSID(t)
	atomic.StoreInt32(&revokeCallCount, 0)

	req, _ := http.NewRequest(http.MethodPost, bffServer.URL+"/bff/logout", nil)
	req.AddCookie(&http.Cookie{Name: "sid", Value: sid})
	client := clientWithCookies(t)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("logout: want 302, got %d", resp.StatusCode)
	}

	// Session must be gone from the store.
	if _, err := store.Get(sid); err == nil {
		t.Error("session should have been deleted after logout")
	}

	// Cookie should be cleared (MaxAge <= 0).
	for _, c := range resp.Cookies() {
		if c.Name == "sid" && c.MaxAge > 0 {
			t.Error("sid cookie should be cleared after logout")
		}
	}

	// Revocation endpoint should have been called.
	if atomic.LoadInt32(&revokeCallCount) == 0 {
		t.Error("expected revocation endpoint to be called")
	}
}

// TestCallbackWithExpiredState verifies that a callback with an unknown state returns 400.
func TestCallbackWithExpiredState(t *testing.T) {
	resp := doRequest(t, "GET", "/bff/callback?code=x&state=no-such-state", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expired state: want 400, got %d", resp.StatusCode)
	}
}

// TestCallbackMissingParams verifies that a callback missing required query params returns 400.
func TestCallbackMissingParams(t *testing.T) {
	resp := doRequest(t, "GET", "/bff/callback", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing params: want 400, got %d", resp.StatusCode)
	}
}

// TestTokenRefreshOnExpiredSession verifies that when the session's access token is
// expired the middleware silently refreshes it and the request succeeds.
func TestTokenRefreshOnExpiredSession(t *testing.T) {
	forgeCapture.reset()

	// Manually plant a session with an already-expired access token.
	sess := &session.Session{
		Sub:          sub1,
		Email:        email1,
		AccessToken:  "expired-token",
		RefreshToken: "test-refresh-token",
		ExpiresAt:    time.Now().Add(-time.Minute), // already expired
	}
	sid, err := store.Create(sess)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	req, _ := http.NewRequest(http.MethodGet, bffServer.URL+"/api/forge/v1/environments", nil)
	req.AddCookie(&http.Cookie{Name: "sid", Value: sid})
	resp, rerr := http.DefaultClient.Do(req)
	if rerr != nil {
		t.Fatalf("api with expired session: %v", rerr)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("token refresh: want 200, got %d", resp.StatusCode)
	}

	// Confirm the session was updated with a fresh token.
	updated, err := store.Get(sid)
	if err != nil {
		t.Fatal("session should still exist after refresh")
	}
	if updated.AccessToken == "expired-token" {
		t.Error("access token should have been updated after refresh")
	}
}

// loginAndGetSID is a test helper that performs the full login flow and returns the
// session ID from the resulting cookie.
func loginAndGetSID(t *testing.T) string {
	t.Helper()
	client := clientWithCookies(t)

	loginResp := doSessionRequest(t, client, http.MethodGet, "/bff/login")
	if loginResp.StatusCode != http.StatusFound {
		t.Fatalf("loginAndGetSID: login: want 302, got %d", loginResp.StatusCode)
	}
	state := parseQueryParam(loginResp.Header.Get("Location"), "state")
	if state == "" {
		t.Fatal("loginAndGetSID: no state in login redirect")
	}

	callbackURL := "/bff/callback?code=test-auth-code&state=" + url.QueryEscape(state)
	cbResp := doSessionRequest(t, client, http.MethodGet, callbackURL)
	if cbResp.StatusCode != http.StatusFound {
		t.Fatalf("loginAndGetSID: callback: want 302, got %d", cbResp.StatusCode)
	}

	sid := sessionCookie(cbResp)
	if sid == "" {
		t.Fatal("loginAndGetSID: no sid cookie after callback")
	}
	return sid
}
