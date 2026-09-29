package handler

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fusion-platform/fusion-bff/internal/api/middleware"
	"github.com/fusion-platform/fusion-bff/internal/db"
)

// ownerGroupNameRE keeps names safe for the comma-separated X-User-Groups header and
// usable as Kubernetes label values on the owned CRs.
var ownerGroupNameRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]{0,61}[A-Za-z0-9])?$`)

// OwnerGroupHandler manages owner groups — the teams that own CRs — together with the
// two ways a user joins one: an OIDC group mapping, or a direct email / user-id match.
type OwnerGroupHandler struct {
	pool         *pgxpool.Pool
	defaultGroup string // every user is implicitly a member; must not be deleted
}

func NewOwnerGroupHandler(pool *pgxpool.Pool, defaultGroup string) *OwnerGroupHandler {
	return &OwnerGroupHandler{pool: pool, defaultGroup: defaultGroup}
}

// ValidOwnerGroupName reports whether name is usable as an owner group name.
func ValidOwnerGroupName(name string) bool { return ownerGroupNameRE.MatchString(name) }

// GET /bff/admin/owner-groups
func (h *OwnerGroupHandler) ListGroups(c *gin.Context) {
	respondList(c, db.ListOwnerGroups, h.pool)
}

// POST /bff/admin/owner-groups  body: {"name":"...", "description":"..."}
func (h *OwnerGroupHandler) CreateGroup(c *gin.Context) {
	var body struct {
		Name        string `json:"name" binding:"required"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	if !ownerGroupNameRE.MatchString(body.Name) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "name must be 1-63 characters: letters, digits, '.', '_' or '-', starting and ending alphanumeric"})
		return
	}
	act := actorFromCtx(c)
	row, err := db.CreateOwnerGroup(c.Request.Context(), h.pool, body.Name, body.Description, act.sub)
	if h.abortOnCreateError(c, err) {
		return
	}
	logAdmin(c, act, "admin: owner group created", "owner_group", row.Name, "id", row.ID)
	c.JSON(http.StatusCreated, row)
}

// DELETE /bff/admin/owner-groups/:id — also removes the group's mappings and members.
func (h *OwnerGroupHandler) DeleteGroup(c *gin.Context) {
	if h.defaultGroup != "" {
		if id, err := strconv.Atoi(c.Param("id")); err == nil {
			row, found, err := db.GetOwnerGroup(c.Request.Context(), h.pool, id)
			if err != nil {
				internalError(c, err)
				return
			}
			if found && row.Name == h.defaultGroup {
				c.AbortWithStatusJSON(http.StatusConflict, gin.H{"error": "the default owner group cannot be deleted"})
				return
			}
		}
	}
	deleteByID(c, h.pool, db.DeleteOwnerGroup, "admin: owner group deleted",
		func(r db.OwnerGroupRow) []any { return []any{"owner_group", r.Name, "id", r.ID} })
}

// GET /bff/admin/owner-group-mappings
func (h *OwnerGroupHandler) ListMappings(c *gin.Context) {
	respondList(c, db.ListOwnerGroupMappings, h.pool)
}

// POST /bff/admin/owner-group-mappings  body: {"owner_group":"...", "oidc_group":"..."}
func (h *OwnerGroupHandler) CreateMapping(c *gin.Context) {
	var body struct {
		OwnerGroup string `json:"owner_group" binding:"required"`
		OIDCGroup  string `json:"oidc_group"  binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "owner_group and oidc_group are required"})
		return
	}
	// Same normalisation the token validator applies to the groups claim.
	oidcGroup := strings.TrimLeft(strings.TrimSpace(body.OIDCGroup), "/")
	if oidcGroup == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "oidc_group must not be empty"})
		return
	}
	act := actorFromCtx(c)
	row, err := db.CreateOwnerGroupMapping(c.Request.Context(), h.pool, body.OwnerGroup, oidcGroup, act.sub)
	if h.abortOnCreateError(c, err) {
		return
	}
	logAdmin(c, act, "admin: owner group mapping created",
		"owner_group", row.OwnerGroup, "oidc_group", row.OIDCGroup, "id", row.ID)
	c.JSON(http.StatusCreated, row)
}

// DELETE /bff/admin/owner-group-mappings/:id
func (h *OwnerGroupHandler) DeleteMapping(c *gin.Context) {
	deleteByID(c, h.pool, db.DeleteOwnerGroupMapping, "admin: owner group mapping deleted",
		func(r db.OwnerGroupMappingRow) []any {
			return []any{"owner_group", r.OwnerGroup, "oidc_group", r.OIDCGroup, "id", r.ID}
		})
}

// GET /bff/admin/owner-group-members
func (h *OwnerGroupHandler) ListMembers(c *gin.Context) {
	respondList(c, db.ListOwnerGroupMembers, h.pool)
}

// POST /bff/admin/owner-group-members
// body: {"owner_group":"...", "match_type":"email"|"user_id", "match_value":"..."}
// user_id is matched against the claim named by OIDC_USER_ID_CLAIM (default "sub").
func (h *OwnerGroupHandler) CreateMember(c *gin.Context) {
	var body struct {
		OwnerGroup string `json:"owner_group" binding:"required"`
		MatchType  string `json:"match_type"  binding:"required"`
		MatchValue string `json:"match_value" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "owner_group, match_type and match_value are required"})
		return
	}
	value := strings.TrimSpace(body.MatchValue)
	switch body.MatchType {
	case db.MatchTypeEmail:
		value = strings.ToLower(value)
		if !strings.Contains(value, "@") {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "match_value must be an email address"})
			return
		}
	case db.MatchTypeUserID:
		if value == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "match_value must not be empty"})
			return
		}
	default:
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "match_type must be email or user_id"})
		return
	}
	act := actorFromCtx(c)
	row, err := db.CreateOwnerGroupMember(c.Request.Context(), h.pool, body.OwnerGroup, body.MatchType, value, act.sub)
	if h.abortOnCreateError(c, err) {
		return
	}
	logAdmin(c, act, "admin: owner group member created",
		"owner_group", row.OwnerGroup, "match_type", row.MatchType, "match_value", row.MatchValue, "id", row.ID)
	c.JSON(http.StatusCreated, row)
}

// DELETE /bff/admin/owner-group-members/:id
func (h *OwnerGroupHandler) DeleteMember(c *gin.Context) {
	deleteByID(c, h.pool, db.DeleteOwnerGroupMember, "admin: owner group member deleted",
		func(r db.OwnerGroupMemberRow) []any {
			return []any{"owner_group", r.OwnerGroup, "match_type", r.MatchType, "match_value", r.MatchValue, "id", r.ID}
		})
}

// abortOnCreateError maps unique / foreign-key violations to 409 / 400 and everything
// else to 500. It reports whether the request was aborted.
func (h *OwnerGroupHandler) abortOnCreateError(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			c.AbortWithStatusJSON(http.StatusConflict, gin.H{"error": "already exists"})
			return true
		case "23503":
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "owner group does not exist"})
			return true
		}
	}
	internalError(c, err)
	return true
}

// respondList writes the rows as JSON, using [] instead of null when empty.
func respondList[T any](c *gin.Context, list func(ctx context.Context, pool *pgxpool.Pool) ([]T, error), pool *pgxpool.Pool) {
	rows, err := list(c.Request.Context(), pool)
	if err != nil {
		internalError(c, err)
		return
	}
	if rows == nil {
		rows = []T{}
	}
	c.JSON(http.StatusOK, rows)
}

// deleteByID parses :id, deletes via del, audit-logs the removed row and answers 204 / 404.
func deleteByID[T any](
	c *gin.Context, pool *pgxpool.Pool,
	del func(ctx context.Context, pool *pgxpool.Pool, id int) (T, bool, error),
	msg string, logAttrs func(T) []any,
) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	row, found, err := del(c.Request.Context(), pool, id)
	if err != nil {
		internalError(c, err)
		return
	}
	if !found {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	logAdmin(c, actorFromCtx(c), msg, logAttrs(row)...)
	c.Status(http.StatusNoContent)
}

// logAdmin writes an audit line carrying the acting admin's identity.
func logAdmin(c *gin.Context, act actor, msg string, attrs ...any) {
	attrs = append([]any{"actor", act.sub, "actor_name", act.name, "actor_groups", act.groups}, attrs...)
	middleware.LoggerFromCtx(c).Info(msg, attrs...)
}
