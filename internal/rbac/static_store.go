package rbac

import "context"

// StaticGroupRoleStore implements GroupRoleStore using the in-memory group_roles and
// user_roles maps from rbac.yaml.
type StaticGroupRoleStore struct {
	groups map[string][]string
	users  map[string][]string
}

func NewStaticGroupRoleStore(groups, users map[string][]string) *StaticGroupRoleStore {
	return &StaticGroupRoleStore{groups: groups, users: users}
}

func (s *StaticGroupRoleStore) RolesForGroup(_ context.Context, group string) ([]string, error) {
	return s.groups[group], nil
}

func (s *StaticGroupRoleStore) RolesForUser(_ context.Context, sub string) ([]string, error) {
	return s.users[sub], nil
}
