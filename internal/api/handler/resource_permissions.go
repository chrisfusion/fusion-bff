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
	"github.com/fusion-platform/fusion-bff/internal/session"
)

// ResourcePermHandler handles CRUD for resource-scoped permission grants.
type ResourcePermHandler struct {
	pool *pgxpool.Pool
}

func NewResourcePermHandler(pool *pgxpool.Pool) *ResourcePermHandler {
	return &ResourcePermHandler{pool: pool}
}

// GET /bff/admin/resource-permissions
func (h *ResourcePermHandler) List(c *gin.Context) {
	rows, err := db.ListResourcePerms(c.Request.Context(), h.pool)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if rows == nil {
		rows = []db.ResourcePermRow{}
	}
	c.JSON(http.StatusOK, rows)
}

// POST /bff/admin/resource-permissions
// body: { subject_type, subject, permission, resource_type, resource_id }
func (h *ResourcePermHandler) Create(c *gin.Context) {
	var body struct {
		SubjectType  string `json:"subject_type"  binding:"required"`
		Subject      string `json:"subject"       binding:"required"`
		Permission   string `json:"permission"    binding:"required"`
		ResourceType string `json:"resource_type" binding:"required"`
		ResourceID   string `json:"resource_id"   binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "all fields are required"})
		return
	}
	if body.SubjectType != "user" && body.SubjectType != "group" && body.SubjectType != "role" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "subject_type must be user, group, or role"})
		return
	}

	createdBy := ""
	if raw, ok := c.Get(middleware.CtxKeySession); ok {
		if sess, ok := raw.(*session.Session); ok {
			createdBy = sess.Sub
		}
	}

	row, err := db.CreateResourcePerm(c.Request.Context(), h.pool,
		body.SubjectType, body.Subject, body.Permission, body.ResourceType, body.ResourceID, createdBy)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			c.AbortWithStatusJSON(http.StatusConflict, gin.H{"error": "grant already exists"})
			return
		}
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusCreated, row)
}

// DELETE /bff/admin/resource-permissions/:id
func (h *ResourcePermHandler) Delete(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	found, err := db.DeleteResourcePerm(c.Request.Context(), h.pool, id)
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
