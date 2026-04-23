package proxy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/fusion-platform/fusion-bff/internal/token"
)

type ctxKey string

const (
	ctxKeyUserID    ctxKey = "fusion-bff/user-id"
	ctxKeyUserEmail ctxKey = "fusion-bff/user-email"
	ctxKeySAToken   ctxKey = "fusion-bff/sa-token"
)

// SetUserContext stores validated user identity in the request context so the
// proxy Rewrite function can inject it as trusted headers without importing Gin.
func SetUserContext(r *http.Request, userID, email string) *http.Request {
	ctx := context.WithValue(r.Context(), ctxKeyUserID, userID)
	ctx = context.WithValue(ctx, ctxKeyUserEmail, email)
	return r.WithContext(ctx)
}

// UpstreamProxy proxies requests to a single upstream service.
// Both forge and index use this type; the difference is the base URL and path prefix.
type UpstreamProxy struct {
	rp      *httputil.ReverseProxy
	saToken token.Provider
}

// NewUpstreamProxy builds an UpstreamProxy that:
//   - strips stripPrefix from the inbound path before forwarding
//   - replaces Authorization with the SA token (fetched per-request in Handler)
//   - injects X-User-ID and X-User-Email from the request context
//   - strips client-supplied identity headers to prevent spoofing
func NewUpstreamProxy(baseURL, stripPrefix string, saToken token.Provider) (*UpstreamProxy, error) {
	target, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parsing upstream URL %q: %w", baseURL, err)
	}

	rp := &httputil.ReverseProxy{
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Del("Access-Control-Allow-Origin")
			resp.Header.Del("Access-Control-Allow-Credentials")
			resp.Header.Del("Access-Control-Allow-Methods")
			resp.Header.Del("Access-Control-Allow-Headers")
			resp.Header.Del("Access-Control-Expose-Headers")
			resp.Header.Del("Access-Control-Max-Age")
			return nil
		},
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)

			// Strip prefix from both Path and RawPath to preserve percent-encoding.
			path := strings.TrimPrefix(pr.In.URL.Path, stripPrefix)
			if path == "" {
				path = "/"
			} else if path[0] != '/' {
				path = "/" + path
			}
			rawPath := ""
			if pr.In.URL.RawPath != "" {
				rawPath = strings.TrimPrefix(pr.In.URL.RawPath, stripPrefix)
				if rawPath == "" {
					rawPath = "/"
				} else if rawPath[0] != '/' {
					rawPath = "/" + rawPath
				}
			}
			pr.Out.URL.Path = path
			pr.Out.URL.RawPath = rawPath

			// Strip client-supplied headers to prevent spoofing.
			pr.Out.Header.Del("X-User-ID")
			pr.Out.Header.Del("X-User-Email")
			pr.Out.Header.Del("Authorization")

			// SA token was pre-fetched in Handler and stored in the request context.
			if tok, ok := pr.In.Context().Value(ctxKeySAToken).(string); ok && tok != "" {
				pr.Out.Header.Set("Authorization", "Bearer "+tok)
			}
			if id, ok := pr.In.Context().Value(ctxKeyUserID).(string); ok && id != "" {
				pr.Out.Header.Set("X-User-ID", id)
			}
			if email, ok := pr.In.Context().Value(ctxKeyUserEmail).(string); ok && email != "" {
				pr.Out.Header.Set("X-User-Email", email)
			}
		},
	}

	return &UpstreamProxy{rp: rp, saToken: saToken}, nil
}

// Handler returns a gin.HandlerFunc that fetches the SA token before proxying.
// If the SA token is unavailable, it returns 502 immediately rather than
// forwarding the request without an Authorization header.
func (u *UpstreamProxy) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		tok, err := u.saToken.Token(c.Request.Context())
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": "upstream unavailable"})
			return
		}
		ctx := context.WithValue(c.Request.Context(), ctxKeySAToken, tok)
		c.Request = c.Request.WithContext(ctx)
		u.rp.ServeHTTP(c.Writer, c.Request)
	}
}
