package rbac

import (
	"context"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fusion-platform/fusion-bff/internal/db"
	"github.com/fusion-platform/fusion-bff/internal/session"
)

// GroupResolver resolves the effective groups for a user from the JWT claim.
// Stage 1 and Stage 2 both use JWTResolver — groups always come from the token.
type GroupResolver interface {
	Resolve(ctx context.Context, sub string, jwtGroups []string) ([]string, error)
}

// JWTResolver passes JWT groups through unchanged.
type JWTResolver struct{}

func (JWTResolver) Resolve(_ context.Context, _ string, jwtGroups []string) ([]string, error) {
	return jwtGroups, nil
}

// Engine resolves a user's roles and permissions from their groups.
type Engine struct {
	resolver  GroupResolver
	roleStore GroupRoleStore
	cfg       *RBACConfig
	pool      *pgxpool.Pool // nil when group_source is "jwt" and no DB configured
}

// NewEngine builds an Engine.
// pool may be nil when group_source is "jwt" (no DB needed).
// group_source "db"   — roles come from DB only (yaml group_roles ignored at runtime).
// group_source "both" — roles are the union of yaml and DB assignments.
// default/"jwt"       — roles come from yaml group_roles only.
// When pool is non-nil, resource-scoped permissions are always resolved from the DB
// regardless of group_source.
func NewEngine(cfg *RBACConfig, pool *pgxpool.Pool) *Engine {
	var roleStore GroupRoleStore
	switch cfg.GroupSource {
	case "db":
		roleStore = NewDBGroupRoleStore(pool)
	case "both":
		roleStore = &MergedGroupRoleStore{
			static:  NewStaticGroupRoleStore(cfg.GroupRoles),
			dbStore: NewDBGroupRoleStore(pool),
		}
	default:
		roleStore = NewStaticGroupRoleStore(cfg.GroupRoles)
	}
	return &Engine{resolver: JWTResolver{}, roleStore: roleStore, cfg: cfg, pool: pool}
}

// Resolve returns the sorted roles and permissions for the given user.
func (e *Engine) Resolve(ctx context.Context, sub string, jwtGroups []string) (roles, permissions []string, err error) {
	groups, err := e.resolver.Resolve(ctx, sub, jwtGroups)
	if err != nil {
		return nil, nil, err
	}

	roleSet := make(map[string]struct{})
	for _, group := range groups {
		groupRoles, rerr := e.roleStore.RolesForGroup(ctx, group)
		if rerr != nil {
			return nil, nil, rerr
		}
		for _, role := range groupRoles {
			roleSet[role] = struct{}{}
		}
	}

	permSet := make(map[string]struct{})
	for role := range roleSet {
		for _, perm := range e.cfg.RolePermissions[role] {
			permSet[perm] = struct{}{}
		}
	}

	for role := range roleSet {
		roles = append(roles, role)
	}
	for perm := range permSet {
		permissions = append(permissions, perm)
	}
	sort.Strings(roles)
	sort.Strings(permissions)
	return roles, permissions, nil
}

// RoutePermissions exposes the config's route rules for use in middleware.
func (e *Engine) RoutePermissions() []RouteRule {
	return e.cfg.RoutePermissions
}

// ResolveResourcePermissions loads all resource-scoped permission grants that apply
// to the user (matched by sub, group membership, or role membership) and expands any
// permission_implies chains defined in the config.
// Returns an empty slice (never nil) when the DB pool is not available.
func (e *Engine) ResolveResourcePermissions(
	ctx context.Context,
	sub string,
	groups []string,
	roles []string,
) ([]session.ResourcePermission, error) {
	if e.pool == nil {
		return []session.ResourcePermission{}, nil
	}

	rows, err := db.LoadResourcePermsForUser(ctx, e.pool, sub, groups, roles)
	if err != nil {
		return nil, err
	}

	type key struct{ permission, resourceType, resourceID string }
	seen := make(map[key]struct{})
	var result []session.ResourcePermission

	add := func(perm, rtype, rid string) {
		k := key{perm, rtype, rid}
		if _, exists := seen[k]; exists {
			return
		}
		seen[k] = struct{}{}
		result = append(result, session.ResourcePermission{
			Permission:   perm,
			ResourceType: rtype,
			ResourceID:   rid,
		})
	}

	for _, row := range rows {
		add(row.Permission, row.ResourceType, row.ResourceID)
		for _, implied := range e.cfg.PermissionImplies[row.Permission] {
			add(implied, row.ResourceType, row.ResourceID)
		}
	}

	if result == nil {
		result = []session.ResourcePermission{}
	}
	return result, nil
}

// RBACConfigSummary returns the static lists of roles, groups, and permissions
// defined in the config. Used by the admin API to power frontend dropdowns.
func (e *Engine) RBACConfigSummary() (roles, groups, permissions []string) {
	seen := make(map[string]struct{})
	for role, perms := range e.cfg.RolePermissions {
		roles = append(roles, role)
		for _, p := range perms {
			if _, ok := seen[p]; !ok {
				seen[p] = struct{}{}
				permissions = append(permissions, p)
			}
		}
	}
	for group := range e.cfg.GroupRoles {
		groups = append(groups, group)
	}
	sort.Strings(roles)
	sort.Strings(groups)
	sort.Strings(permissions)
	return
}
