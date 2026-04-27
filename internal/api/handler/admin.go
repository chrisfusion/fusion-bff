package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fusion-platform/fusion-bff/internal/api/middleware"
	"github.com/fusion-platform/fusion-bff/internal/db"
	"github.com/fusion-platform/fusion-bff/internal/rbac"
	"github.com/fusion-platform/fusion-bff/internal/session"
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
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
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

	createdBy := ""
	if raw, ok := c.Get(middleware.CtxKeySession); ok {
		if sess, ok := raw.(*session.Session); ok {
			createdBy = sess.Sub
		}
	}

	row, err := db.CreateGroupRole(c.Request.Context(), h.pool, body.Group, body.Role, createdBy)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			c.AbortWithStatusJSON(http.StatusConflict, gin.H{"error": "assignment already exists"})
			return
		}
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
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
	found, err := db.DeleteGroupRole(c.Request.Context(), h.pool, id)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if !found {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.Status(http.StatusNoContent)
}
