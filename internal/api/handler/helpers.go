package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fusion-platform/fusion-bff/internal/api/middleware"
)

func internalError(c *gin.Context, err error) {
	middleware.LoggerFromCtx(c).Error("internal error", "error", err)
	c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
}
