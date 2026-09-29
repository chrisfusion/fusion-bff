package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/fusion-platform/fusion-bff/internal/api/middleware"
	"github.com/fusion-platform/fusion-bff/internal/rbac"
	"github.com/fusion-platform/fusion-bff/internal/session"
)

func preferencesRouter(t *testing.T) (*gin.Engine, *http.Cookie) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store := session.NewInMemoryStore(time.Hour)
	sid, err := store.Create(&session.Session{Sub: "s", UserID: "u1", OwnerGroups: []string{"default", "team-x"}})
	if err != nil {
		t.Fatal(err)
	}
	engine := rbac.NewEngine(&rbac.RBACConfig{}, nil).WithDefaultOwnerGroup("default")
	h := NewPreferencesHandler(engine, store)
	r := gin.New()
	g := r.Group("/bff/preferences", middleware.SessionAuth(store, "sid", ""))
	g.GET("", h.Get)
	g.PUT("", h.Put)
	g.DELETE("", h.Delete)
	return r, &http.Cookie{Name: "sid", Value: sid}
}

func TestPreferencesHandler_NoDB(t *testing.T) {
	r, cookie := preferencesRouter(t)

	do := func(method, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/bff/preferences", strings.NewReader(body))
		req.AddCookie(cookie)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	if w := do("GET", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"effective":"default"`) {
		t.Errorf("GET: %d %s", w.Code, w.Body)
	}
	if w := do("PUT", `{"preferred_owner_group":"team-x"}`); w.Code != http.StatusServiceUnavailable {
		t.Errorf("PUT without DB: %d, want 503", w.Code)
	}
	if w := do("DELETE", ""); w.Code != http.StatusServiceUnavailable {
		t.Errorf("DELETE without DB: %d, want 503", w.Code)
	}
	if w := do("PUT", `{}`); w.Code != http.StatusBadRequest {
		t.Errorf("PUT empty body: %d, want 400", w.Code)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/bff/preferences", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("no cookie: %d, want 401", w.Code)
	}
}
