package rbac

import "context"

// GroupRoleStore maps group names to role names.
// The Engine uses this to resolve group→role during authentication.
type GroupRoleStore interface {
	RolesForGroup(ctx context.Context, group string) ([]string, error)
}
