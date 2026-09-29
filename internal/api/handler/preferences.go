package handler

import (
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"

	"github.com/fusion-platform/fusion-bff/internal/api/middleware"
	"github.com/fusion-platform/fusion-bff/internal/rbac"
	"github.com/fusion-platform/fusion-bff/internal/session"
)

// PreferencesHandler serves the calling user's own preferences (currently the preferred
// owner group the GUI preselects). Identity always comes from the session, never the request.
type PreferencesHandler struct {
	engine *rbac.Engine
	store  session.Store
}

func NewPreferencesHandler(engine *rbac.Engine, store session.Store) *PreferencesHandler {
	return &PreferencesHandler{engine: engine, store: store}
}

func (h *PreferencesHandler) view(sess *session.Session) gin.H {
	var stored any
	if sess.PreferredOwnerGroup != "" {
		stored = sess.PreferredOwnerGroup
	}
	return gin.H{
		"preferred_owner_group": stored,
		"effective":             preferredOwnerGroup(h.engine, sess),
	}
}

// GET /bff/preferences → {"preferred_owner_group": stored|null, "effective": group|null}
func (h *PreferencesHandler) Get(c *gin.Context) {
	sess := sessionFromCtx(c)
	c.JSON(http.StatusOK, h.view(sess))
}

// PUT /bff/preferences  body: {"preferred_owner_group":"..."}
func (h *PreferencesHandler) Put(c *gin.Context) {
	sess := sessionFromCtx(c)
	var body struct {
		PreferredOwnerGroup string `json:"preferred_owner_group" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "preferred_owner_group is required"})
		return
	}
	if !h.persistable(c, sess) {
		return
	}
	if !slices.Contains(sess.OwnerGroups, body.PreferredOwnerGroup) {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "not a member of that owner group"})
		return
	}
	if err := h.engine.SetPreferredOwnerGroup(c.Request.Context(), sess.UserID, body.PreferredOwnerGroup); err != nil {
		internalError(c, err)
		return
	}
	h.updateSession(c, sess, body.PreferredOwnerGroup)
}

// DELETE /bff/preferences — clears the stored preference (the default group applies again).
func (h *PreferencesHandler) Delete(c *gin.Context) {
	sess := sessionFromCtx(c)
	if !h.persistable(c, sess) {
		return
	}
	if err := h.engine.ClearPreferredOwnerGroup(c.Request.Context(), sess.UserID); err != nil {
		internalError(c, err)
		return
	}
	h.updateSession(c, sess, "")
}

// persistable aborts with 503 (no DB) or 400 (token carries no user id) when a write cannot be stored.
func (h *PreferencesHandler) persistable(c *gin.Context, sess *session.Session) bool {
	if !h.engine.PreferencesEnabled() {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "preferences require a database (DB_DSN)"})
		return false
	}
	if sess.UserID == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "token carries no user id claim; preferences cannot be stored"})
		return false
	}
	return true
}

// updateSession applies the new preference to the live session so it takes effect immediately.
func (h *PreferencesHandler) updateSession(c *gin.Context, sess *session.Session, preferred string) {
	sess.PreferredOwnerGroup = preferred
	if err := h.store.Update(sess); err != nil {
		// Session vanished concurrently (logout); the preference itself is stored.
		middleware.LoggerFromCtx(c).Warn("preferences: session update", "error", err)
	}
	c.JSON(http.StatusOK, h.view(sess))
}

// sessionFromCtx returns the session attached by SessionAuth.
func sessionFromCtx(c *gin.Context) *session.Session {
	raw, _ := c.Get(middleware.CtxKeySession)
	sess, _ := raw.(*session.Session)
	return sess
}
