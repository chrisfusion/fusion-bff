package rbac

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// RBACConfig is the top-level structure loaded from rbac.yaml.
type RBACConfig struct {
	GroupSource      string              `yaml:"group_source"`
	GroupRoles       map[string][]string `yaml:"group_roles"`
	RolePermissions  map[string][]string `yaml:"role_permissions"`
	RoutePermissions []RouteRule         `yaml:"route_permissions"`
	// PermissionImplies defines cascading grants: if a resource-scoped grant exists
	// for a key permission, the value permissions are also granted on the same resource.
	PermissionImplies map[string][]string `yaml:"permission_implies"`
}

// RouteRule maps an HTTP method + path pattern to a required permission string.
type RouteRule struct {
	Method       string `yaml:"method"`
	Path         string `yaml:"path"`
	Permission   string `yaml:"permission"`
	ResourceType string `yaml:"resource_type,omitempty"` // non-empty enables resource-scoped fallback
}

// LoadConfig reads and parses the YAML config at path.
func LoadConfig(path string) (*RBACConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("rbac: read config %q: %w", path, err)
	}
	var cfg RBACConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("rbac: parse config: %w", err)
	}
	if cfg.GroupSource == "" {
		cfg.GroupSource = "jwt"
	}
	return &cfg, nil
}

// GroupNames returns the keys of the group_roles map.
// Used to populate the mock OIDC login form selector.
func (c *RBACConfig) GroupNames() []string {
	names := make([]string, 0, len(c.GroupRoles))
	for g := range c.GroupRoles {
		names = append(names, g)
	}
	return names
}
