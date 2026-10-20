package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fusion-platform/fusion-bff/internal/api/middleware"
	"github.com/fusion-platform/fusion-bff/internal/db"
	"github.com/fusion-platform/fusion-bff/internal/rbac"
)

// AdminHandler handles BFF-native admin API endpoints.
type AdminHandler struct {
	pool   *pgxpool.Pool
	engine *rbac.Engine
}

func NewAdminHandler(pool *pgxpool.Pool, engine *rbac.Engine) *AdminHandler {
	return &AdminHandler{pool: pool, engine: engine}
}

// GET /bff/admin/group-roles
func (h *AdminHandler) ListGroupRoles(c *gin.Context) {
	rows, err := db.ListGroupRoles(c.Request.Context(), h.pool)
	if err != nil {
		internalError(c, err)
		return
	}
	if rows == nil {
		rows = []db.GroupRoleRow{}
	}
	c.JSON(http.StatusOK, rows)
}

// POST /bff/admin/group-roles  body: {"group":"...", "role":"..."}
func (h *AdminHandler) CreateGroupRole(c *gin.Context) {
	var body struct {
		Group string `json:"group" binding:"required"`
		Role  string `json:"role"  binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "group and role are required"})
		return
	}

	act := actorFromCtx(c)

	row, err := db.CreateGroupRole(c.Request.Context(), h.pool, body.Group, body.Role, act.sub)
	if err != nil {
		if isUniqueViolation(err) {
			c.AbortWithStatusJSON(http.StatusConflict, gin.H{"error": "assignment already exists"})
			return
		}
		internalError(c, err)
		return
	}
	middleware.LoggerFromCtx(c).Info("admin: group-role assignment created",
		"actor", act.sub, "actor_name", act.name, "actor_groups", act.groups,
		"group", row.GroupName, "role", row.RoleName, "id", row.ID)
	c.JSON(http.StatusCreated, row)
}

// GET /bff/admin/rbac-config
// Returns the static lists of roles, groups, and permissions from the RBAC config.
// Powers dropdowns in the frontend admin forms.
func (h *AdminHandler) RBACConfig(c *gin.Context) {
	roles, groups, permissions := h.engine.RBACConfigSummary()
	c.JSON(http.StatusOK, gin.H{
		"roles":       roles,
		"groups":      groups,
		"permissions": permissions,
	})
}

// DELETE /bff/admin/group-roles/:id
func (h *AdminHandler) DeleteGroupRole(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	row, found, err := db.DeleteGroupRole(c.Request.Context(), h.pool, id)
	if err != nil {
		internalError(c, err)
		return
	}
	if !found {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	act := actorFromCtx(c)
	middleware.LoggerFromCtx(c).Info("admin: group-role assignment deleted",
		"actor", act.sub, "actor_name", act.name, "actor_groups", act.groups,
		"group", row.GroupName, "role", row.RoleName, "id", row.ID,
		"originally_created_by", row.CreatedBy, "originally_created_at", row.CreatedAt)
	c.Status(http.StatusNoContent)
}

// GET /bff/admin/user-roles
func (h *AdminHandler) ListUserRoles(c *gin.Context) {
	rows, err := db.ListUserRoles(c.Request.Context(), h.pool)
	if err != nil {
		internalError(c, err)
		return
	}
	if rows == nil {
		rows = []db.UserRoleRow{}
	}
	c.JSON(http.StatusOK, rows)
}

// POST /bff/admin/user-roles  body: {"sub":"...", "role":"..."}
// Assigns a role directly to one user (OIDC sub), for users not covered by any group.
func (h *AdminHandler) CreateUserRole(c *gin.Context) {
	var body struct {
		Sub  string `json:"sub"  binding:"required"`
		Role string `json:"role" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Sub) == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "sub and role are required"})
		return
	}

	act := actorFromCtx(c)

	row, err := db.CreateUserRole(c.Request.Context(), h.pool, strings.TrimSpace(body.Sub), body.Role, act.sub)
	if err != nil {
		if isUniqueViolation(err) {
			c.AbortWithStatusJSON(http.StatusConflict, gin.H{"error": "assignment already exists"})
			return
		}
		internalError(c, err)
		return
	}
	middleware.LoggerFromCtx(c).Info("admin: user-role assignment created",
		"actor", act.sub, "actor_name", act.name, "actor_groups", act.groups,
		"user_sub", row.UserSub, "role", row.RoleName, "id", row.ID)
	c.JSON(http.StatusCreated, row)
}

// DELETE /bff/admin/user-roles/:id
func (h *AdminHandler) DeleteUserRole(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	row, found, err := db.DeleteUserRole(c.Request.Context(), h.pool, id)
	if err != nil {
		internalError(c, err)
		return
	}
	if !found {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	act := actorFromCtx(c)
	middleware.LoggerFromCtx(c).Info("admin: user-role assignment deleted",
		"actor", act.sub, "actor_name", act.name, "actor_groups", act.groups,
		"user_sub", row.UserSub, "role", row.RoleName, "id", row.ID,
		"originally_created_by", row.CreatedBy, "originally_created_at", row.CreatedAt)
	c.Status(http.StatusNoContent)
}
