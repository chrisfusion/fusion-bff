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

// List filters (weave/wizard listquery parameters) must reach the upstream byte for byte:
// repeated keys, commas and encoded "+" in RFC 3339 offsets included.
func TestUpstreamProxy_ForwardsQueryString(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var gotPath, gotQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
	}))
	defer upstream.Close()

	p, err := NewUpstreamProxy(upstream.URL, "/api/weave", staticToken("sa"))
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.Any("/api/weave/*path", p.Handler())
	front := httptest.NewServer(r)
	defer front.Close()

	const query = "phase=Running,Failed&phase=Pending&source=gitops&since=2026-10-27T13%3A00%3A00%2B01%3A00&sort=-created&limit=50&continue=bzE6NTA"
	resp, err := http.Get(front.URL + "/api/weave/api/v1/runs?" + query)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if gotPath != "/api/v1/runs" || gotQuery != query {
		t.Errorf("upstream got %s?%s, want /api/v1/runs?%s", gotPath, gotQuery, query)
	}
}
