package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

const loggerKey = "slog_logger"

// NewLoggingMiddleware honours or generates an X-Request-ID, stamps a per-request
// *slog.Logger with {request_id, method, client_ip}, stores it in gin.Context, and
// logs the access line (status + latency + resolved route path) after the handler returns.
func NewLoggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		reqID := c.GetHeader("X-Request-ID")
		if reqID == "" {
			b := make([]byte, 16)
			_, _ = rand.Read(b)
			reqID = hex.EncodeToString(b)
		}
		c.Header("X-Request-ID", reqID)

		logger := slog.Default().With(
			"request_id", reqID,
			"method", c.Request.Method,
			"client_ip", c.ClientIP(),
		)
		c.Set(loggerKey, logger)

		c.Next()

		// c.FullPath() returns the matched route template (e.g. /api/forge/*path)
		// only after Gin has dispatched the request; falls back to raw URL for 404s.
		path := c.FullPath()
		if path == "" {
			path = c.Request.URL.Path
		}

		logger.Info("request",
			"path", path,
			"status", c.Writer.Status(),
			"latency_ms", time.Since(start).Milliseconds(),
		)
	}
}

// LoggerFromCtx returns the per-request logger set by NewLoggingMiddleware.
// Falls back to slog.Default() if the middleware was not applied.
func LoggerFromCtx(c *gin.Context) *slog.Logger {
	v, exists := c.Get(loggerKey)
	if !exists {
		return slog.Default()
	}
	if logger, ok := v.(*slog.Logger); ok {
		return logger
	}
	return slog.Default()
}
