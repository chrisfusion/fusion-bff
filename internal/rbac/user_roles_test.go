package rbac

import (
	"context"
	"slices"
	"testing"
)

func TestResolve_UserRolesAddedToGroupRoles(t *testing.T) {
	cfg := &RBACConfig{
		GroupSource: "jwt",
		GroupRoles:  map[string][]string{"team-data": {"engineer"}},
		UserRoles:   map[string][]string{"sub-special": {"admin"}},
		RolePermissions: map[string][]string{
			"engineer": {"forge:builds:read"},
			"admin":    {"forge:builds:read", "admin:roles:manage"},
		},
	}
	e := NewEngine(cfg, nil)

	tests := []struct {
		name      string
		sub       string
		groups    []string
		wantRoles []string
		wantPerms []string
	}{
		{"group only", "sub-other", []string{"team-data"}, []string{"engineer"}, []string{"forge:builds:read"}},
		{"sub only, no groups", "sub-special", nil, []string{"admin"}, []string{"admin:roles:manage", "forge:builds:read"}},
		{"sub and group merged", "sub-special", []string{"team-data"}, []string{"admin", "engineer"}, []string{"admin:roles:manage", "forge:builds:read"}},
		{"unknown user", "sub-nobody", []string{"team-x"}, nil, nil},
		{"empty sub", "", nil, nil, nil},
	}
	for _, tt := range tests {
		roles, perms, err := e.Resolve(context.Background(), tt.sub, tt.groups)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if !slices.Equal(roles, tt.wantRoles) {
			t.Errorf("%s: roles = %v, want %v", tt.name, roles, tt.wantRoles)
		}
		if !slices.Equal(perms, tt.wantPerms) {
			t.Errorf("%s: permissions = %v, want %v", tt.name, perms, tt.wantPerms)
		}
	}
}
