package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

type staticToken string

func (s staticToken) Token(context.Context) (string, error) { return string(s), nil }

func TestUpstreamProxy_ForwardsOwnerGroupsAndStripsSpoofedHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var got http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
	}))
	defer upstream.Close()

	p, err := NewUpstreamProxy(upstream.URL, "/api/x", staticToken("sa"))
	if err != nil {
		t.Fatal(err)
	}

	run := func(groups []string) http.Header {
		got = nil
		r := gin.New()
		r.Any("/api/x/*path", func(c *gin.Context) {
			c.Request = SetUserContext(c.Request, "u1", "u@x.io", groups)
			p.Handler()(c)
		})
		front := httptest.NewServer(r)
		defer front.Close()
		req, _ := http.NewRequest(http.MethodGet, front.URL+"/api/x/things", nil)
		req.Header.Set("X-User-Groups", "spoofed")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return got
	}

	if h := run([]string{"team-a", "team-b"}); h.Get("X-User-Groups") != "team-a,team-b" {
		t.Errorf("X-User-Groups = %q, want team-a,team-b", h.Get("X-User-Groups"))
	}
	if h := run(nil); h.Get("X-User-Groups") != "" {
		t.Errorf("spoofed X-User-Groups leaked upstream: %q", h.Get("X-User-Groups"))
	}
}
