package rbac

import (
	"fmt"
	"testing"
)

func TestMatchCapture(t *testing.T) {
	rules := []RouteRule{
		{Method: "DELETE", Path: "/api/index/api/v1/artifacts/*/versions/*", Permission: "index:versions:delete", ResourceType: "artifact"},
		{Method: "DELETE", Path: "/api/index/api/v1/artifacts/*",            Permission: "index:artifacts:delete", ResourceType: "artifact"},
	}
	cases := []struct {
		method, path string
		wantPerm     string
		wantID       string
	}{
		{"DELETE", "/api/index/api/v1/artifacts/351",            "index:artifacts:delete", "351"},
		{"DELETE", "/api/index/api/v1/artifacts/351/versions/1.0.0", "index:versions:delete", "351"},
		{"DELETE", "/api/index/api/v1/artifacts/999",            "index:artifacts:delete", "999"},
	}
	for _, c := range cases {
		m := MatchRoute(rules, c.method, c.path)
		fmt.Printf("%-55s → perm=%-30s type=%-10s id=%s\n", c.path, m.Permission, m.ResourceType, m.ResourceID)
		if m.Permission != c.wantPerm {
			t.Errorf("%s: perm want %s got %s", c.path, c.wantPerm, m.Permission)
		}
		if m.ResourceID != c.wantID {
			t.Errorf("%s: id want %s got %s", c.path, c.wantID, m.ResourceID)
		}
	}
}
