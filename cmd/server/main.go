package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/oauth2"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/fusion-platform/fusion-bff/internal/allowlist"
	"github.com/fusion-platform/fusion-bff/internal/api"
	"github.com/fusion-platform/fusion-bff/internal/api/handler"
	"github.com/fusion-platform/fusion-bff/internal/config"
	"github.com/fusion-platform/fusion-bff/internal/db"
	"github.com/fusion-platform/fusion-bff/internal/mockoidc"
	"github.com/fusion-platform/fusion-bff/internal/oidc"
	"github.com/fusion-platform/fusion-bff/internal/proxy"
	"github.com/fusion-platform/fusion-bff/internal/rbac"
	"github.com/fusion-platform/fusion-bff/internal/session"
	"github.com/fusion-platform/fusion-bff/internal/token"
)

func main() {
	// Must be first — ensures startup errors are emitted in the configured format.
	setupLogger(os.Getenv("LOG_LEVEL"), os.Getenv("LOG_FORMAT"))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "error", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	rbacCfg, err := rbac.LoadConfig(cfg.RBACConfigPath)
	if err != nil {
		slog.Error("rbac config", "error", err)
		os.Exit(1)
	}

	// Open DB pool whenever DB_DSN is set (used by RBAC store and system health overrides).
	var pool *pgxpool.Pool
	if cfg.DBDSN != "" {
		var err error
		pool, err = db.Open(ctx, cfg.DBDSN)
		if err != nil {
			slog.Error("db", "error", err)
			os.Exit(1)
		}
		defer pool.Close()
		if err := db.Migrate(ctx, pool); err != nil {
			slog.Error("db migrate", "error", err)
			os.Exit(1)
		}
	}

	var adminH *handler.AdminHandler
	var resourcePermH *handler.ResourcePermHandler
	var rbacEngine *rbac.Engine

	if rbacCfg.GroupSource == "db" || rbacCfg.GroupSource == "both" {
		if pool == nil {
			slog.Error("DB_DSN is required when rbac group_source is set", "group_source", rbacCfg.GroupSource)
			os.Exit(1)
		}
		rbacEngine = rbac.NewEngine(rbacCfg, pool)
		adminH = handler.NewAdminHandler(pool, rbacEngine)
		resourcePermH = handler.NewResourcePermHandler(pool)
	} else {
		rbacEngine = rbac.NewEngine(rbacCfg, nil)
		// adminH and resourcePermH stay nil — NewRouter skips the /bff/admin RBAC group
	}

	// SystemHealthHandler is always constructed — live probing works without DB;
	// overrides are skipped when pool is nil.
	systemHealthH := handler.NewSystemHealthHandler(
		pool, // may be nil when DB_DSN is unset
		cfg.ForgeHealthURL,
		cfg.IndexHealthURL,
		cfg.WeaveHealthURL,
		cfg.ContentHealthURL,
		cfg.HealthProbeTimeout,
	)

	var validator oidc.TokenValidator
	var checker allowlist.Checker
	var mockOIDC *mockoidc.Server

	if cfg.OIDCBypass {
		slog.Warn("OIDC_BYPASS=true — embedded mock OIDC active — NOT for production use")
		mockOIDC = mockoidc.New(cfg, rbacCfg.GroupNames())
		validator = mockOIDC.Validator()
		checker = allowlist.New(nil)
	} else {
		validator, err = oidc.NewValidator(ctx, cfg.OIDCIssuerURL, cfg.OIDCClientID, cfg.OIDCJWKSURL, cfg.JWKSCacheTTL)
		if err != nil {
			slog.Error("oidc validator", "error", err)
			os.Exit(1)
		}
		checker = allowlist.New(cfg.AllowedUsers)
	}

	store := session.NewInMemoryStore(cfg.SessionMaxAge)
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				store.Reap()
			case <-ctx.Done():
				return
			}
		}
	}()

	oauthCfg := &oauth2.Config{
		ClientID:     cfg.OIDCClientID,
		ClientSecret: cfg.OIDCClientSecret,
		Endpoint: oauth2.Endpoint{
			TokenURL: cfg.OIDCIssuerURL + "/protocol/openid-connect/token",
		},
	}
	refreshFn := func(rCtx context.Context, refreshToken string) (*oauth2.Token, error) {
		src := oauthCfg.TokenSource(rCtx, &oauth2.Token{
			RefreshToken: refreshToken,
			Expiry:       time.Now().Add(-time.Second),
		})
		return src.Token()
	}

	authH := handler.NewAuthHandler(cfg, store, validator, checker, rbacEngine)

	saToken := token.NewFileProvider(cfg.SATokenPath, cfg.SATokenCacheTTL)
	weaveSAToken := token.NewFileProvider(cfg.WeaveSATokenPath, cfg.SATokenCacheTTL)

	forgeProxy, err := proxy.NewUpstreamProxy(cfg.ForgeURL, "/api/forge", saToken)
	if err != nil {
		slog.Error("forge proxy", "error", err)
		os.Exit(1)
	}
	indexProxy, err := proxy.NewUpstreamProxy(cfg.IndexURL, "/api/index", saToken)
	if err != nil {
		slog.Error("index proxy", "error", err)
		os.Exit(1)
	}
	weaveProxy, err := proxy.NewUpstreamProxy(cfg.WeaveURL, "/api/weave", weaveSAToken)
	if err != nil {
		slog.Error("weave proxy", "error", err)
		os.Exit(1)
	}
	contentProxy, err := proxy.NewUpstreamProxy(cfg.ContentURL, "/api/content", saToken)
	if err != nil {
		slog.Error("content proxy", "error", err)
		os.Exit(1)
	}

	router := api.NewRouter(validator, checker, authH, store, refreshFn, cfg, rbacEngine, forgeProxy, indexProxy, weaveProxy, contentProxy, adminH, resourcePermH, systemHealthH)
	if mockOIDC != nil {
		mockOIDC.RegisterRoutes(router)
	}

	srv := &http.Server{
		Addr:    ":" + cfg.HTTPPort,
		Handler: router,
	}

	go func() {
		<-ctx.Done()
		slog.Info("shutting down")
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer shutCancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			slog.Error("shutdown", "error", err)
		}
	}()

	slog.Info("starting server", "addr", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server", "error", err)
		os.Exit(1)
	}
}

func setupLogger(logLevel, logFormat string) {
	var level slog.Level
	unknownLevel := false
	switch logLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	case "info", "":
		level = slog.LevelInfo
	default:
		level = slog.LevelInfo
		unknownLevel = true
	}

	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if logFormat == "text" {
		h = slog.NewTextHandler(os.Stdout, opts)
	} else {
		h = slog.NewJSONHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(h))

	if unknownLevel {
		slog.Warn("unrecognised LOG_LEVEL, defaulting to info", "value", logLevel)
	}
}
