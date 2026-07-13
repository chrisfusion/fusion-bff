package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fusion-platform/fusion-bff/internal/presets"
)

// PresetsHandler serves this unit's static infrastructure presets (Kafka
// clusters, secret names) to the frontend, so creation wizards can offer a
// dropdown instead of requiring the exact resource name to be typed.
type PresetsHandler struct {
	cfg *presets.Config
}

func NewPresetsHandler(cfg *presets.Config) *PresetsHandler {
	return &PresetsHandler{cfg: cfg}
}

// GET /bff/presets
func (h *PresetsHandler) List(c *gin.Context) {
	c.JSON(http.StatusOK, h.cfg)
}
