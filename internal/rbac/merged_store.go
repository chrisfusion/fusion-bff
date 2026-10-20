package rbac

import "context"

// MergedGroupRoleStore returns the union of roles from static yaml and DB assignments
// (for groups and for individual users alike).
// Used when group_source is "both" — yaml roles are always present, DB adds extras.
type MergedGroupRoleStore struct {
	static *StaticGroupRoleStore
	dbStore *DBGroupRoleStore
}

func (m *MergedGroupRoleStore) RolesForGroup(ctx context.Context, group string) ([]string, error) {
	return mergeRoles(ctx, group, m.static.RolesForGroup, m.dbStore.RolesForGroup)
}

func (m *MergedGroupRoleStore) RolesForUser(ctx context.Context, sub string) ([]string, error) {
	return mergeRoles(ctx, sub, m.static.RolesForUser, m.dbStore.RolesForUser)
}

type rolesFunc func(ctx context.Context, key string) ([]string, error)

func mergeRoles(ctx context.Context, key string, static, dbStore rolesFunc) ([]string, error) {
	a, err := static(ctx, key)
	if err != nil {
		return nil, err
	}
	b, err := dbStore(ctx, key)
	if err != nil {
		return nil, err
	}
	return unionStrings(a, b), nil
}

func unionStrings(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	result := make([]string, 0, len(a)+len(b))
	for _, s := range append(a, b...) {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			result = append(result, s)
		}
	}
	return result
}
