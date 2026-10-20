package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/fusion-platform/fusion-bff/internal/api/middleware"
	"github.com/fusion-platform/fusion-bff/internal/session"
)

func internalError(c *gin.Context, err error) {
	middleware.LoggerFromCtx(c).Error("internal error", "error", err)
	c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
}

// isUniqueViolation reports whether err is a PostgreSQL unique-constraint violation.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// actor identifies who performed an admin action, for audit logging.
type actor struct {
	sub    string
	name   string
	groups []string
}

// actorFromCtx reads the calling admin's identity off the session stored in
// gin.Context by SessionAuth. Zero-value fields when no session is present.
// groups is always non-nil so it logs as "[]" rather than "null".
func actorFromCtx(c *gin.Context) actor {
	raw, ok := c.Get(middleware.CtxKeySession)
	if !ok {
		return actor{groups: []string{}}
	}
	sess, ok := raw.(*session.Session)
	if !ok {
		return actor{groups: []string{}}
	}
	groups := sess.Groups
	if groups == nil {
		groups = []string{}
	}
	return actor{sub: sess.Sub, name: sess.Name, groups: groups}
}
