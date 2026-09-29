package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Owner groups are the teams that own CRs (steps, runs, ...). They are unrelated to
// OIDC groups → roles; a user joins an owner group through a matching OIDC group
// (owner_group_oidc_mappings) or a direct match on email / user id (owner_group_members).

// Member match types.
const (
	MatchTypeEmail  = "email"
	MatchTypeUserID = "user_id"
)

type OwnerGroupRow struct {
	ID          int       `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
}

type OwnerGroupMappingRow struct {
	ID         int       `json:"id"`
	OwnerGroup string    `json:"owner_group"`
	OIDCGroup  string    `json:"oidc_group"`
	CreatedBy  string    `json:"created_by"`
	CreatedAt  time.Time `json:"created_at"`
}

type OwnerGroupMemberRow struct {
	ID         int       `json:"id"`
	OwnerGroup string    `json:"owner_group"`
	MatchType  string    `json:"match_type"`
	MatchValue string    `json:"match_value"`
	CreatedBy  string    `json:"created_by"`
	CreatedAt  time.Time `json:"created_at"`
}

// listRows runs query and scans every row with scan.
func listRows[T any](ctx context.Context, pool *pgxpool.Pool, query string, scan func(pgx.Rows, *T) error) ([]T, error) {
	rows, err := pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []T
	for rows.Next() {
		var r T
		if err := scan(rows, &r); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// deleteReturning runs a `DELETE ... RETURNING` statement; found is false when no row matched.
func deleteReturning[T any](ctx context.Context, pool *pgxpool.Pool, query string, id int, scan func(pgx.Row, *T) error) (row T, found bool, err error) {
	if err = scan(pool.QueryRow(ctx, query, id), &row); err != nil {
		var zero T
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, false, nil
		}
		return zero, false, err
	}
	return row, true, nil
}

func scanOwnerGroup(r pgx.Row, o *OwnerGroupRow) error {
	return r.Scan(&o.ID, &o.Name, &o.Description, &o.CreatedBy, &o.CreatedAt)
}

func scanOwnerGroupMapping(r pgx.Row, o *OwnerGroupMappingRow) error {
	return r.Scan(&o.ID, &o.OwnerGroup, &o.OIDCGroup, &o.CreatedBy, &o.CreatedAt)
}

func scanOwnerGroupMember(r pgx.Row, o *OwnerGroupMemberRow) error {
	return r.Scan(&o.ID, &o.OwnerGroup, &o.MatchType, &o.MatchValue, &o.CreatedBy, &o.CreatedAt)
}

const (
	ownerGroupCols = `id, name, description, created_by, created_at`
	mappingCols    = `id, owner_group, oidc_group, created_by, created_at`
	memberCols     = `id, owner_group, match_type, match_value, created_by, created_at`
)

// ── Owner groups ──────────────────────────────────────────────────────────────

func ListOwnerGroups(ctx context.Context, pool *pgxpool.Pool) ([]OwnerGroupRow, error) {
	return listRows(ctx, pool, `SELECT `+ownerGroupCols+` FROM owner_groups ORDER BY name`,
		func(r pgx.Rows, o *OwnerGroupRow) error { return scanOwnerGroup(r, o) })
}

func CreateOwnerGroup(ctx context.Context, pool *pgxpool.Pool, name, description, createdBy string) (OwnerGroupRow, error) {
	var o OwnerGroupRow
	err := scanOwnerGroup(pool.QueryRow(ctx,
		`INSERT INTO owner_groups (name, description, created_by) VALUES ($1, $2, $3) RETURNING `+ownerGroupCols,
		name, description, createdBy), &o)
	return o, err
}

// DeleteOwnerGroup also removes the group's mappings and members (ON DELETE CASCADE).
func DeleteOwnerGroup(ctx context.Context, pool *pgxpool.Pool, id int) (OwnerGroupRow, bool, error) {
	return deleteReturning(ctx, pool, `DELETE FROM owner_groups WHERE id = $1 RETURNING `+ownerGroupCols, id, scanOwnerGroup)
}

// ── OIDC group → owner group mappings ─────────────────────────────────────────

func ListOwnerGroupMappings(ctx context.Context, pool *pgxpool.Pool) ([]OwnerGroupMappingRow, error) {
	return listRows(ctx, pool, `SELECT `+mappingCols+` FROM owner_group_oidc_mappings ORDER BY owner_group, oidc_group`,
		func(r pgx.Rows, o *OwnerGroupMappingRow) error { return scanOwnerGroupMapping(r, o) })
}

func CreateOwnerGroupMapping(ctx context.Context, pool *pgxpool.Pool, ownerGroup, oidcGroup, createdBy string) (OwnerGroupMappingRow, error) {
	var o OwnerGroupMappingRow
	err := scanOwnerGroupMapping(pool.QueryRow(ctx,
		`INSERT INTO owner_group_oidc_mappings (owner_group, oidc_group, created_by) VALUES ($1, $2, $3) RETURNING `+mappingCols,
		ownerGroup, oidcGroup, createdBy), &o)
	return o, err
}

func DeleteOwnerGroupMapping(ctx context.Context, pool *pgxpool.Pool, id int) (OwnerGroupMappingRow, bool, error) {
	return deleteReturning(ctx, pool, `DELETE FROM owner_group_oidc_mappings WHERE id = $1 RETURNING `+mappingCols, id, scanOwnerGroupMapping)
}

// ── Direct user → owner group members ─────────────────────────────────────────

func ListOwnerGroupMembers(ctx context.Context, pool *pgxpool.Pool) ([]OwnerGroupMemberRow, error) {
	return listRows(ctx, pool, `SELECT `+memberCols+` FROM owner_group_members ORDER BY owner_group, match_type, match_value`,
		func(r pgx.Rows, o *OwnerGroupMemberRow) error { return scanOwnerGroupMember(r, o) })
}

// CreateOwnerGroupMember stores a direct assignment. Callers must pass an already
// normalised matchValue (emails lower-cased) — see LoadOwnerGroupsForUser.
func CreateOwnerGroupMember(ctx context.Context, pool *pgxpool.Pool, ownerGroup, matchType, matchValue, createdBy string) (OwnerGroupMemberRow, error) {
	var o OwnerGroupMemberRow
	err := scanOwnerGroupMember(pool.QueryRow(ctx,
		`INSERT INTO owner_group_members (owner_group, match_type, match_value, created_by) VALUES ($1, $2, $3, $4) RETURNING `+memberCols,
		ownerGroup, matchType, matchValue, createdBy), &o)
	return o, err
}

func DeleteOwnerGroupMember(ctx context.Context, pool *pgxpool.Pool, id int) (OwnerGroupMemberRow, bool, error) {
	return deleteReturning(ctx, pool, `DELETE FROM owner_group_members WHERE id = $1 RETURNING `+memberCols, id, scanOwnerGroupMember)
}

// LoadOwnerGroupsForUser returns the sorted, de-duplicated names of every owner group
// the user belongs to: via one of their OIDC groups, or a direct email / user id match.
// Email comparison is case-insensitive (stored values are lower-cased).
func LoadOwnerGroupsForUser(ctx context.Context, pool *pgxpool.Pool, userID, email string, oidcGroups []string) ([]string, error) {
	if oidcGroups == nil {
		oidcGroups = []string{}
	}
	rows, err := pool.Query(ctx,
		`SELECT owner_group FROM owner_group_oidc_mappings WHERE oidc_group = ANY($3)
		 UNION
		 SELECT owner_group FROM owner_group_members
		  WHERE (match_type = 'email'   AND $2 <> '' AND match_value = lower($2))
		     OR (match_type = 'user_id' AND $1 <> '' AND match_value = $1)
		 ORDER BY 1`,
		userID, email, oidcGroups)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	names := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}
