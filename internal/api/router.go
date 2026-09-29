package api

import (
	"context"

	"github.com/gin-gonic/gin"
	"golang.org/x/oauth2"

	"github.com/fusion-platform/fusion-bff/internal/allowlist"
	"github.com/fusion-platform/fusion-bff/internal/api/handler"
	"github.com/fusion-platform/fusion-bff/internal/api/middleware"
	"github.com/fusion-platform/fusion-bff/internal/config"
	"github.com/fusion-platform/fusion-bff/internal/docs"
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
	forge   *proxy.UpstreamProxy,
	index   *proxy.UpstreamProxy,
	weave   *proxy.UpstreamProxy,
	wizard  *proxy.UpstreamProxy,
	content *proxy.UpstreamProxy,
	adminH *handler.AdminHandler,
	resourcePermH *handler.ResourcePermHandler,
	systemHealthH *handler.SystemHealthHandler,
	presetsH *handler.PresetsHandler,
	ownerGroupH *handler.OwnerGroupHandler,
	preferencesH *handler.PreferencesHandler,
) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.NewLoggingMiddleware())
	r.Use(middleware.CORS(cfg.CORSOrigins))

	r.GET("/health", handler.Health)
	r.GET("/livez", handler.Livez)
	r.GET("/readyz", handler.Readyz)

	bff := r.Group("/bff")
	bff.GET("/openapi.yaml", docs.SpecHandler)
	bff.GET("/docs/*any", docs.UIHandler())
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

	if ownerGroupH != nil {
		ownerGroupAdmin := bff.Group("/admin")
		ownerGroupAdmin.Use(middleware.SessionAuth(store, cfg.SessionCookieName, "admin:roles:manage"))
		ownerGroupAdmin.GET("/owner-groups", ownerGroupH.ListGroups)
		ownerGroupAdmin.POST("/owner-groups", ownerGroupH.CreateGroup)
		ownerGroupAdmin.DELETE("/owner-groups/:id", ownerGroupH.DeleteGroup)
		ownerGroupAdmin.GET("/owner-group-mappings", ownerGroupH.ListMappings)
		ownerGroupAdmin.POST("/owner-group-mappings", ownerGroupH.CreateMapping)
		ownerGroupAdmin.DELETE("/owner-group-mappings/:id", ownerGroupH.DeleteMapping)
		ownerGroupAdmin.GET("/owner-group-members", ownerGroupH.ListMembers)
		ownerGroupAdmin.POST("/owner-group-members", ownerGroupH.CreateMember)
		ownerGroupAdmin.DELETE("/owner-group-members/:id", ownerGroupH.DeleteMember)
	}

	if preferencesH != nil {
		prefs := bff.Group("/preferences", middleware.SessionAuth(store, cfg.SessionCookieName, ""))
		prefs.GET("", preferencesH.Get)
		prefs.PUT("", preferencesH.Put)
		prefs.DELETE("", preferencesH.Delete)
	}

	if presetsH != nil {
		bff.GET("/presets", middleware.SessionAuth(store, cfg.SessionCookieName, "bff:presets:read"), presetsH.List)
	}

	if systemHealthH != nil {
		bff.GET("/system-health", middleware.SessionAuth(store, cfg.SessionCookieName, ""), systemHealthH.Status)

		healthAdminGroup := bff.Group("/admin")
		healthAdminGroup.Use(middleware.SessionAuth(store, cfg.SessionCookieName, "admin:health:manage"))
		healthAdminGroup.GET("/service-status", systemHealthH.ListOverrides)
		healthAdminGroup.PUT("/service-status/:service", systemHealthH.UpsertOverride)
		healthAdminGroup.DELETE("/service-status/:service", systemHealthH.DeleteOverride)
	}

	api := r.Group("/api")
	api.Use(middleware.APIAuth(store, refreshFn, validator, checker, cfg, engine))
	api.Any("/forge/*path", forge.Handler())
	api.Any("/index/*path", index.Handler())
	api.Any("/weave/*path", weave.Handler())
	api.Any("/wizard/*path", wizard.Handler())
	api.Any("/content/*path", content.Handler())

	return r
}
