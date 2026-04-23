//go:build e2e

package e2e

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type jwtClaims struct {
	Issuer   string   `json:"iss"`
	Audience []string `json:"aud"`
	Subject  string   `json:"sub"`
	Email    string   `json:"email"`
	IssuedAt int64    `json:"iat"`
	Expiry   int64    `json:"exp"`
}

func mintJWT(t *testing.T, key *rsa.PrivateKey, kid string, claims jwtClaims) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": kid, "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	msg := b64url(header) + "." + b64url(payload)
	h := crypto.SHA256.New()
	h.Write([]byte(msg))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h.Sum(nil))
	if err != nil {
		t.Fatalf("mintJWT: sign: %v", err)
	}
	return msg + "." + b64url(sig)
}

func b64url(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

// revokeCallCount counts how many times the revocation endpoint has been called.
// Tests reset it with atomic.StoreInt32(&revokeCallCount, 0).
var revokeCallCount int32

func newOIDCServerGlobal(key *rsa.PrivateKey, kid, clientID string) *httptest.Server {
	jwks := buildJWKS(key, kid)
	mux := http.NewServeMux()

	mux.HandleFunc("/protocol/openid-connect/certs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jwks)
	})

	// Token endpoint: handles authorization_code exchange and refresh_token grants.
	// Issues a real signed ID token so validator.Validate succeeds in the callback handler.
	mux.HandleFunc("/protocol/openid-connect/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		grantType := r.FormValue("grant_type")
		var sub, email string
		switch grantType {
		case "authorization_code", "refresh_token":
			sub, email = sub1, email1
		default:
			http.Error(w, `{"error":"unsupported_grant_type"}`, http.StatusBadRequest)
			return
		}

		// The server URL is not available here; use the package-level oidcSrv once set.
		// During TestMain the oidcSrv variable is set before any requests are made.
		issuer := ""
		if oidcSrv != nil {
			issuer = oidcSrv.URL
		}
		idClaims := validClaims(issuer, clientID, sub, email)
		idToken := mustMintJWT(key, kid, idClaims)
		accessToken := mustMintJWT(key, kid, validClaims(issuer, clientID, sub, email))

		expiry := time.Now().Add(5 * time.Minute).Unix()
		resp := map[string]interface{}{
			"access_token":  accessToken,
			"token_type":    "Bearer",
			"refresh_token": "test-refresh-token",
			"id_token":      idToken,
			"expires_in":    300,
			"expiry":        expiry,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	// Revocation endpoint: records calls so tests can assert it was invoked.
	mux.HandleFunc("/protocol/openid-connect/revoke", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&revokeCallCount, 1)
		w.WriteHeader(http.StatusOK)
	})

	// End-session endpoint: accepts logout redirects.
	mux.HandleFunc("/protocol/openid-connect/logout", func(w http.ResponseWriter, r *http.Request) {
		redirect := r.URL.Query().Get("post_logout_redirect_uri")
		if redirect == "" {
			redirect = "/"
		}
		http.Redirect(w, r, redirect, http.StatusFound)
	})

	return httptest.NewServer(mux)
}

// mustMintJWT mints a JWT, panicking on error (for use in test server handlers).
func mustMintJWT(key *rsa.PrivateKey, kid string, claims jwtClaims) string {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": kid, "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	msg := b64url(header) + "." + b64url(payload)
	h := crypto.SHA256.New()
	h.Write([]byte(msg))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h.Sum(nil))
	if err != nil {
		panic("mustMintJWT: " + err.Error())
	}
	return msg + "." + b64url(sig)
}

// parseQueryParam extracts a named query parameter from a URL string.
func parseQueryParam(rawURL, param string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Query().Get(param)
}

func newUpstreamServerGlobal(capture *capturedRequest) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if capture != nil {
			capture.set(r)
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"ok":true}`)
	}))
}

func buildJWKS(key *rsa.PrivateKey, kid string) map[string]interface{} {
	pub := key.Public().(*rsa.PublicKey)
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes())
	return map[string]interface{}{
		"keys": []map[string]interface{}{
			{"kty": "RSA", "use": "sig", "alg": "RS256", "kid": kid, "n": n, "e": e},
		},
	}
}

// capturedRequest holds the last request received by a mock upstream server.
// All fields are protected by a mutex because the HTTP server handler and test
// goroutines access them concurrently.
type capturedRequest struct {
	mu      sync.Mutex
	method  string
	path    string
	headers http.Header
}

func (c *capturedRequest) set(r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.method = r.Method
	c.path = r.URL.Path
	c.headers = r.Header.Clone()
}

func (c *capturedRequest) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.method, c.path, c.headers = "", "", nil
}

func (c *capturedRequest) get() (method, path string, headers http.Header) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.method, c.path, c.headers.Clone()
}

func validClaims(issuer, clientID, sub, email string) jwtClaims {
	now := time.Now()
	return jwtClaims{
		Issuer:   issuer,
		Audience: []string{clientID},
		Subject:  sub,
		Email:    email,
		IssuedAt: now.Unix(),
		Expiry:   now.Add(5 * time.Minute).Unix(),
	}
}
