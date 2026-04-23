package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/oauth2"

	"github.com/fusion-platform/fusion-bff/internal/allowlist"
	"github.com/fusion-platform/fusion-bff/internal/api"
	"github.com/fusion-platform/fusion-bff/internal/api/handler"
	"github.com/fusion-platform/fusion-bff/internal/config"
	"github.com/fusion-platform/fusion-bff/internal/mockoidc"
	"github.com/fusion-platform/fusion-bff/internal/oidc"
	"github.com/fusion-platform/fusion-bff/internal/proxy"
	"github.com/fusion-platform/fusion-bff/internal/session"
	"github.com/fusion-platform/fusion-bff/internal/token"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var validator oidc.TokenValidator
	var checker allowlist.Checker
	var mockOIDC *mockoidc.Server

	if cfg.OIDCBypass {
		log.Println("[WARNING] OIDC_BYPASS=true — embedded mock OIDC active — NOT for production use")
		mockOIDC = mockoidc.New(cfg)
		validator = mockOIDC.Validator()
		checker = allowlist.New(nil) // allow all authenticated users; bypass is the gate
	} else {
		var err error
		validator, err = oidc.NewValidator(ctx, cfg.OIDCIssuerURL, cfg.OIDCClientID, cfg.OIDCJWKSURL, cfg.JWKSCacheTTL)
		if err != nil {
			log.Fatalf("oidc validator: %v", err)
		}
		// Use the static in-memory checker directly; TTL cache only adds value
		// when the Checker implementation performs I/O (e.g. database lookup).
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
			Expiry:       time.Now().Add(-time.Second), // force refresh
		})
		return src.Token()
	}

	authH := handler.NewAuthHandler(cfg, store, validator, checker)

	saToken := token.NewFileProvider(cfg.SATokenPath, cfg.SATokenCacheTTL)
	weaveSAToken := token.NewFileProvider(cfg.WeaveSATokenPath, cfg.SATokenCacheTTL)

	forgeProxy, err := proxy.NewUpstreamProxy(cfg.ForgeURL, "/api/forge", saToken)
	if err != nil {
		log.Fatalf("forge proxy: %v", err)
	}

	indexProxy, err := proxy.NewUpstreamProxy(cfg.IndexURL, "/api/index", saToken)
	if err != nil {
		log.Fatalf("index proxy: %v", err)
	}

	weaveProxy, err := proxy.NewUpstreamProxy(cfg.WeaveURL, "/api/weave", weaveSAToken)
	if err != nil {
		log.Fatalf("weave proxy: %v", err)
	}

	router := api.NewRouter(validator, checker, authH, store, refreshFn, cfg, forgeProxy, indexProxy, weaveProxy)
	if mockOIDC != nil {
		mockOIDC.RegisterRoutes(router)
	}

	srv := &http.Server{
		Addr:    ":" + cfg.HTTPPort,
		Handler: router,
	}

	go func() {
		<-ctx.Done()
		log.Println("shutting down")
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer shutCancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}()

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server: %v", err)
	}
}
