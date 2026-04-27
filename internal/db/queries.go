package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type GroupRoleRow struct {
	ID        int       `json:"id"`
	GroupName string    `json:"group_name"`
	RoleName  string    `json:"role_name"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

func ListGroupRoles(ctx context.Context, pool *pgxpool.Pool) ([]GroupRoleRow, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, group_name, role_name, created_by, created_at
		 FROM group_role_assignments
		 ORDER BY group_name, role_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []GroupRoleRow
	for rows.Next() {
		var r GroupRoleRow
		if err := rows.Scan(&r.ID, &r.GroupName, &r.RoleName, &r.CreatedBy, &r.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func CreateGroupRole(ctx context.Context, pool *pgxpool.Pool, group, role, createdBy string) (GroupRoleRow, error) {
	var r GroupRoleRow
	err := pool.QueryRow(ctx,
		`INSERT INTO group_role_assignments (group_name, role_name, created_by)
		 VALUES ($1, $2, $3)
		 RETURNING id, group_name, role_name, created_by, created_at`,
		group, role, createdBy,
	).Scan(&r.ID, &r.GroupName, &r.RoleName, &r.CreatedBy, &r.CreatedAt)
	return r, err
}

func DeleteGroupRole(ctx context.Context, pool *pgxpool.Pool, id int) (bool, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM group_role_assignments WHERE id = $1`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// LoadAllGroupRoles returns a map[group_name][]role_name for use in DBGroupRoleStore.
func LoadAllGroupRoles(ctx context.Context, pool *pgxpool.Pool) (map[string][]string, error) {
	rows, err := pool.Query(ctx, `SELECT group_name, role_name FROM group_role_assignments`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	m := make(map[string][]string)
	for rows.Next() {
		var group, role string
		if err := rows.Scan(&group, &role); err != nil {
			return nil, err
		}
		m[group] = append(m[group], role)
	}
	return m, rows.Err()
}

// ── Resource permissions ──────────────────────────────────────────────────────

type ResourcePermRow struct {
	ID           int       `json:"id"`
	SubjectType  string    `json:"subject_type"`
	Subject      string    `json:"subject"`
	Permission   string    `json:"permission"`
	ResourceType string    `json:"resource_type"`
	ResourceID   string    `json:"resource_id"`
	CreatedBy    string    `json:"created_by"`
	CreatedAt    time.Time `json:"created_at"`
}

func ListResourcePerms(ctx context.Context, pool *pgxpool.Pool) ([]ResourcePermRow, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, subject_type, subject, permission, resource_type, resource_id, created_by, created_at
		 FROM resource_permissions
		 ORDER BY resource_type, resource_id, subject_type, subject`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ResourcePermRow
	for rows.Next() {
		var r ResourcePermRow
		if err := rows.Scan(&r.ID, &r.SubjectType, &r.Subject, &r.Permission,
			&r.ResourceType, &r.ResourceID, &r.CreatedBy, &r.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func CreateResourcePerm(ctx context.Context, pool *pgxpool.Pool,
	subjectType, subject, permission, resourceType, resourceID, createdBy string,
) (ResourcePermRow, error) {
	var r ResourcePermRow
	err := pool.QueryRow(ctx,
		`INSERT INTO resource_permissions
		    (subject_type, subject, permission, resource_type, resource_id, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 RETURNING id, subject_type, subject, permission, resource_type, resource_id, created_by, created_at`,
		subjectType, subject, permission, resourceType, resourceID, createdBy,
	).Scan(&r.ID, &r.SubjectType, &r.Subject, &r.Permission,
		&r.ResourceType, &r.ResourceID, &r.CreatedBy, &r.CreatedAt)
	return r, err
}

func DeleteResourcePerm(ctx context.Context, pool *pgxpool.Pool, id int) (bool, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM resource_permissions WHERE id = $1`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// LoadResourcePermsForUser returns all resource_permissions rows that apply to the
// given user — matching by sub (subject_type='user'), any of their groups, or any of their roles.
func LoadResourcePermsForUser(ctx context.Context, pool *pgxpool.Pool,
	sub string, groups, roles []string,
) ([]ResourcePermRow, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, subject_type, subject, permission, resource_type, resource_id, created_by, created_at
		 FROM resource_permissions
		 WHERE (subject_type = 'user'  AND subject = $1)
		    OR (subject_type = 'group' AND subject = ANY($2))
		    OR (subject_type = 'role'  AND subject = ANY($3))`,
		sub, groups, roles,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ResourcePermRow
	for rows.Next() {
		var r ResourcePermRow
		if err := rows.Scan(&r.ID, &r.SubjectType, &r.Subject, &r.Permission,
			&r.ResourceType, &r.ResourceID, &r.CreatedBy, &r.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}
