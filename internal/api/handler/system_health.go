package handler

import (
	"context"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fusion-platform/fusion-bff/internal/api/middleware"
	"github.com/fusion-platform/fusion-bff/internal/db"
	"github.com/fusion-platform/fusion-bff/internal/session"
)

var validServiceStatuses = map[string]bool{
	"Healthy":     true,
	"Unhealthy":   true,
	"Offline":     true,
	"Maintenance": true,
}

var validServices = map[string]bool{
	"forge": true, "index": true, "weave": true, "spectra": true,
}

type SystemHealthHandler struct {
	pool           *pgxpool.Pool // may be nil when DB_DSN is unset
	client         *http.Client
	forgeHealthURL string
	indexHealthURL string
	weaveHealthURL string
}

func NewSystemHealthHandler(pool *pgxpool.Pool, forgeHealthURL, indexHealthURL, weaveHealthURL string, timeout time.Duration) *SystemHealthHandler {
	return &SystemHealthHandler{
		pool:           pool,
		client:         &http.Client{Timeout: timeout},
		forgeHealthURL: forgeHealthURL,
		indexHealthURL: indexHealthURL,
		weaveHealthURL: weaveHealthURL,
	}
}

type liveResult struct {
	Reachable  bool   `json:"reachable"`
	StatusCode *int   `json:"status_code,omitempty"`
	LatencyMs  *int64 `json:"latency_ms,omitempty"`
	Error      string `json:"error,omitempty"`
}

type serviceStatus struct {
	Name     string               `json:"name"`
	Live     *liveResult          `json:"live"`
	Override *db.ServiceStatusRow `json:"override"`
}

// GET /bff/system-health — available to all authenticated users.
func (h *SystemHealthHandler) Status(c *gin.Context) {
	ctx := c.Request.Context()

	overrideMap := make(map[string]*db.ServiceStatusRow)
	if h.pool != nil {
		overrides, err := db.ListServiceStatuses(ctx, h.pool)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		for i := range overrides {
			overrideMap[overrides[i].Service] = &overrides[i]
		}
	}

	type probeTarget struct {
		name string
		url  string
	}
	targets := []probeTarget{
		{"forge", h.forgeHealthURL},
		{"index", h.indexHealthURL},
		{"weave", h.weaveHealthURL},
	}

	results := make([]liveResult, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func(i int, url string) {
			defer wg.Done()
			results[i] = h.probe(ctx, url)
		}(i, t.url)
	}
	wg.Wait()

	services := make([]serviceStatus, 0, len(targets)+1)
	for i, t := range targets {
		lr := results[i]
		services = append(services, serviceStatus{
			Name:     t.name,
			Live:     &lr,
			Override: overrideMap[t.name],
		})
	}
	// Spectra is manual-only — no live probe.
	services = append(services, serviceStatus{
		Name:     "spectra",
		Live:     nil,
		Override: overrideMap["spectra"],
	})

	c.JSON(http.StatusOK, gin.H{"services": services})
}

func (h *SystemHealthHandler) probe(ctx context.Context, url string) liveResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return liveResult{Reachable: false, Error: "probe failed"}
	}
	start := time.Now()
	resp, err := h.client.Do(req)
	latencyMs := time.Since(start).Milliseconds()
	if err != nil {
		log.Printf("health probe %s: %v", url, err)
		return liveResult{Reachable: false, Error: "probe failed"}
	}
	defer func() {
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()
	}()
	code := resp.StatusCode
	reachable := code < 500
	return liveResult{
		Reachable:  reachable,
		StatusCode: &code,
		LatencyMs:  &latencyMs,
	}
}

// GET /bff/admin/service-status
func (h *SystemHealthHandler) ListOverrides(c *gin.Context) {
	if h.pool == nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "database not configured"})
		return
	}
	rows, err := db.ListServiceStatuses(c.Request.Context(), h.pool)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if rows == nil {
		rows = []db.ServiceStatusRow{}
	}
	c.JSON(http.StatusOK, rows)
}

// PUT /bff/admin/service-status/:service
func (h *SystemHealthHandler) UpsertOverride(c *gin.Context) {
	if h.pool == nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "database not configured"})
		return
	}
	service := c.Param("service")
	if !validServices[service] {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "unknown service"})
		return
	}
	var body struct {
		Status      string `json:"status" binding:"required"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "status is required"})
		return
	}
	if !validServiceStatuses[body.Status] {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "status must be Healthy, Unhealthy, Offline, or Maintenance"})
		return
	}

	updatedBy := ""
	if raw, ok := c.Get(middleware.CtxKeySession); ok {
		if sess, ok := raw.(*session.Session); ok {
			updatedBy = sess.Sub
		}
	}

	row, err := db.UpsertServiceStatus(c.Request.Context(), h.pool, service, body.Status, body.Description, updatedBy)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, row)
}

// DELETE /bff/admin/service-status/:service
func (h *SystemHealthHandler) DeleteOverride(c *gin.Context) {
	if h.pool == nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "database not configured"})
		return
	}
	service := c.Param("service")
	if !validServices[service] {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "unknown service"})
		return
	}
	found, err := db.DeleteServiceStatus(c.Request.Context(), h.pool, service)
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
