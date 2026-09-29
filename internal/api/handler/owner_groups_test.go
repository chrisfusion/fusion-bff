package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// Validation runs before any DB access, so a nil pool is enough to exercise the 400 paths.
func TestOwnerGroupHandler_Validation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewOwnerGroupHandler(nil)
	r := gin.New()
	r.POST("/groups", h.CreateGroup)
	r.POST("/mappings", h.CreateMapping)
	r.POST("/members", h.CreateMember)
	r.DELETE("/groups/:id", h.DeleteGroup)

	tests := []struct{ name, method, path, body string }{
		{"group without name", "POST", "/groups", `{}`},
		{"group with comma", "POST", "/groups", `{"name":"a,b"}`},
		{"group with leading dash", "POST", "/groups", `{"name":"-a"}`},
		{"group name too long", "POST", "/groups", `{"name":"` + strings.Repeat("a", 64) + `"}`},
		{"mapping missing oidc_group", "POST", "/mappings", `{"owner_group":"a"}`},
		{"mapping slash-only oidc_group", "POST", "/mappings", `{"owner_group":"a","oidc_group":"/"}`},
		{"member bad type", "POST", "/members", `{"owner_group":"a","match_type":"name","match_value":"x"}`},
		{"member email without @", "POST", "/members", `{"owner_group":"a","match_type":"email","match_value":"x"}`},
		{"member blank user_id", "POST", "/members", `{"owner_group":"a","match_type":"user_id","match_value":"  "}`},
		{"delete non-numeric id", "DELETE", "/groups/abc", ``},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body)))
			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 (body %s)", w.Code, w.Body)
			}
		})
	}
}

func TestOwnerGroupNameRE(t *testing.T) {
	for _, ok := range []string{"a", "team-data", "Team_1.x", strings.Repeat("a", 63)} {
		if !ownerGroupNameRE.MatchString(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "a b", "a,b", ".a", "a.", "ä"} {
		if ownerGroupNameRE.MatchString(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}
