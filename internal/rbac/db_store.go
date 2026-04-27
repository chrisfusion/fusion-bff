package rbac

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fusion-platform/fusion-bff/internal/db"
)

// DBGroupRoleStore implements GroupRoleStore using PostgreSQL.
// Each call to RolesForGroup does a full table scan; the table is expected to be tiny.
type DBGroupRoleStore struct {
	pool *pgxpool.Pool
}

func NewDBGroupRoleStore(pool *pgxpool.Pool) *DBGroupRoleStore {
	return &DBGroupRoleStore{pool: pool}
}

func (s *DBGroupRoleStore) RolesForGroup(ctx context.Context, group string) ([]string, error) {
	m, err := db.LoadAllGroupRoles(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	return m[group], nil
}
