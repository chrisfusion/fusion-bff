package api

import (
	"context"

	"github.com/gin-gonic/gin"
	"golang.org/x/oauth2"

	"github.com/fusion-platform/fusion-bff/internal/allowlist"
	"github.com/fusion-platform/fusion-bff/internal/api/handler"
	"github.com/fusion-platform/fusion-bff/internal/api/middleware"
	"github.com/fusion-platform/fusion-bff/internal/config"
	"github.com/fusion-platform/fusion-bff/internal/oidc"
	"github.com/fusion-platform/fusion-bff/internal/proxy"
	"github.com/fusion-platform/fusion-bff/internal/rbac"
	"github.com/fusion-platform/fusion-bff/internal/session"
)

func NewRouter(
	validator oidc.TokenValidator,
	checker allowlist.Checker,
	authH *handler.AuthHandler,
	store session.Store,
	refreshFn func(ctx context.Context, refreshToken string) (*oauth2.Token, error),
	cfg *config.Config,
	engine *rbac.Engine,
	forge *proxy.UpstreamProxy,
	index *proxy.UpstreamProxy,
	weave *proxy.UpstreamProxy,
	adminH *handler.AdminHandler,
	resourcePermH *handler.ResourcePermHandler,
) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(gin.Logger())
	r.Use(middleware.RequestID())
	r.Use(middleware.CORS(cfg.CORSOrigins))

	r.GET("/health", handler.Health)
	r.GET("/livez", handler.Livez)
	r.GET("/readyz", handler.Readyz)

	bff := r.Group("/bff")
	bff.GET("/login", authH.Login)
	bff.GET("/callback", authH.Callback)
	bff.POST("/logout", authH.Logout)
	bff.GET("/userinfo", authH.UserInfo)

	if adminH != nil {
		adminGroup := bff.Group("/admin")
		adminGroup.Use(middleware.SessionAuth(store, cfg.SessionCookieName, "admin:roles:manage"))
		adminGroup.GET("/group-roles", adminH.ListGroupRoles)
		adminGroup.POST("/group-roles", adminH.CreateGroupRole)
		adminGroup.DELETE("/group-roles/:id", adminH.DeleteGroupRole)
		adminGroup.GET("/rbac-config", adminH.RBACConfig)
		if resourcePermH != nil {
			adminGroup.GET("/resource-permissions", resourcePermH.List)
			adminGroup.POST("/resource-permissions", resourcePermH.Create)
			adminGroup.DELETE("/resource-permissions/:id", resourcePermH.Delete)
		}
	}

	api := r.Group("/api")
	api.Use(middleware.APIAuth(store, refreshFn, validator, checker, cfg, engine))
	api.Any("/forge/*path", forge.Handler())
	api.Any("/index/*path", index.Handler())
	api.Any("/weave/*path", weave.Handler())

	return r
}
