package rbac

import "context"

// StaticGroupRoleStore implements GroupRoleStore using the in-memory map from rbac.yaml.
type StaticGroupRoleStore struct {
	m map[string][]string
}

func NewStaticGroupRoleStore(m map[string][]string) *StaticGroupRoleStore {
	return &StaticGroupRoleStore{m: m}
}

func (s *StaticGroupRoleStore) RolesForGroup(_ context.Context, group string) ([]string, error) {
	return s.m[group], nil
}
