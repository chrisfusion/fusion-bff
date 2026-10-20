package rbac

import "context"

// GroupRoleStore maps group names to role names, plus individual users (by OIDC sub)
// for the few exceptions that cannot be expressed through OIDC groups.
// The Engine uses this to resolve group→role and sub→role during authentication.
type GroupRoleStore interface {
	RolesForGroup(ctx context.Context, group string) ([]string, error)
	RolesForUser(ctx context.Context, sub string) ([]string, error)
}
