# Architecture

## Overview

`fusion-bff` is a thin, stateless reverse proxy. Its only responsibilities are:

1. Validate an OIDC JWT issued to the GUI
2. Check the resolved identity against an allowlist
3. Replace the inbound JWT with the BFF's own K8s service account token
4. Forward the resolved user identity as trusted headers
5. Proxy the request to the appropriate upstream service

There is no database, no session state, and no business logic.

---

## Component map

```
┌────────────────────────────────────────────────────────────┐
│  fusion-bff (this service)                                 │
│                                                            │
│  cmd/server/main.go                                        │
│    └─ config.Load()             reads env vars             │
│    └─ oidc.NewValidator()       JWKS-backed JWT verifier   │
│    └─ allowlist.New()           in-memory sub/email checker│
│    └─ token.NewFileProvider() ×2  (saToken, weaveSAToken) │
│    └─ proxy.NewUpstreamProxy() ×3  (forge, index, weave)  │
│    └─ api.NewRouter()           gin engine                 │
│    └─ http.Server.ListenAndServe()                         │
│                                                            │
│  internal/api/router.go                                    │
│    /health  /livez  /readyz   → handler.Health (no auth)  │
│    /api/forge/*path           → middleware.Auth → forge    │
│    /api/index/*path           → middleware.Auth → index    │
│    /api/weave/*path           → middleware.Auth → weave    │
│                                                            │
│  internal/api/middleware/auth.go                           │
│    1. Extract Bearer token from Authorization header       │
│    2. oidc.Validate()  →  UserClaims{Subject, Email}      │
│    3. allowlist.Permitted(sub, email)                      │
│    4. proxy.SetUserContext(r, sub, email)                  │
│                                                            │
│  internal/proxy/upstream.go                               │
│    Handler()  pre-fetches SA token, stores in ctx         │
│    Rewrite()  strips /api/{forge|index|weave} prefix      │
│               deletes inbound X-User-ID / X-User-Email    │
│               sets Authorization: Bearer <SA token>       │
│               sets X-User-ID / X-User-Email from ctx      │
└────────────────────────────────────────────────────────────┘
          │               │               │
          ▼               ▼               ▼
  fusion-forge:8080  fusion-index-  fusion-weave-api:8082
  (SA token +         backend:8080   (SA token via TokenReview
   X-User-ID,         (same)          + X-User-ID, X-User-Email;
   X-User-Email)                       RBAC role from SA label)
```

---

## Request lifecycle

```
GUI
 │  GET /api/forge/v1/venvs
 │  Authorization: Bearer <OIDC JWT>
 │
 ▼
gin.Recovery + gin.Logger + middleware.RequestID
 │
 ▼
middleware.Auth
 ├─ parse Bearer token from header
 ├─ oidcValidator.Validate()
 │    └─ cachingKeySet.VerifySignature()   (JWKS, cached)
 │    └─ check exp, iss, aud
 │    └─ extract sub + email
 ├─ staticChecker.Permitted(sub, email)
 │    └─ 403 if denied
 └─ proxy.SetUserContext(r, sub, email)
 │
 ▼
proxy.UpstreamProxy.Handler()
 ├─ token.FileProvider.Token()             (disk read, cached)
 │    └─ 502 on error
 ├─ store SA token in ctx
 └─ httputil.ReverseProxy.ServeHTTP()
      └─ Rewrite:
           strip /api/forge prefix  →  /v1/venvs
           del X-User-ID, X-User-Email, Authorization (anti-spoofing)
           set Authorization: Bearer <SA token>
           set X-User-ID: <sub>
           set X-User-Email: <email>
 │
 ▼
fusion-forge  GET /v1/venvs
              Authorization: Bearer <SA token>
              X-User-ID: alice-sub-123
              X-User-Email: alice@example.com
```

---

## Key design decisions

### No OIDC provider discovery at startup

`NewValidator` does not call the OIDC discovery endpoint (`/.well-known/openid-configuration`). It accepts an explicit `OIDC_JWKS_URL`, which defaults to the Keycloak convention (`{issuer}/protocol/openid-connect/certs`). This avoids a startup dependency on the OIDC provider being reachable and makes the service compatible with any OIDC provider without special casing.

### JWKS caching with force-refresh

`cachingKeySet` wraps `coreos/go-oidc`'s `RemoteKeySet`. The cache is invalidated after `OIDC_JWKS_CACHE_TTL` (default 15 min). A background goroutine is not used — the refresh happens synchronously on the first request after TTL expiry. This keeps the implementation simple and avoids background goroutine lifecycle management.

### SA token pre-fetched before proxying

`httputil.ReverseProxy.Rewrite` cannot return an error. To handle SA token fetch failures cleanly, the token is fetched in `Handler()` before `rp.ServeHTTP` is called. On failure the handler returns `502 upstream unavailable` immediately.

### Separate SA token per upstream (audience isolation)

forge/index and fusion-weave use distinct projected SA tokens:

| Volume | Audience | Used for |
|---|---|---|
| `sa-token` | `fusion-bff` | forge, index (dev mode; no token validation on their side yet) |
| `weave-sa-token` | *(none — kube-apiserver default)* | fusion-weave (TokenReview validates token against kube-apiserver) |

A projected token with `audience: fusion-bff` fails Kubernetes TokenReview when no audience is specified in the review request — the API server validates the token against its own audiences, which do not include `fusion-bff`. Omitting the audience in the projected token source makes it valid for the kube-apiserver and thus compatible with TokenReview.

### User identity via `context.Context`, not Gin context

The proxy `Rewrite` function receives `*httputil.ProxyRequest`, not a Gin context. Passing identity through `context.WithValue` on the `*http.Request` avoids importing Gin into the `proxy` package and keeps the dependency graph clean.

### Anti-spoofing header deletion

The `Rewrite` function unconditionally deletes `X-User-ID`, `X-User-Email`, and `Authorization` from the outbound request before setting them from the validated context. This ensures a malicious client cannot inject trusted identity headers.

### `staticChecker` is not wrapped in `WithTTLCache`

`WithTTLCache` is designed for checkers that perform I/O (e.g. a database lookup). The static in-memory checker has no I/O cost — adding a TTL cache would only introduce unnecessary complexity and memory allocation.

---

## Package structure

```
cmd/server/          Entry point — wires all components and starts the HTTP server
internal/
  config/            Env var loading; all time.Duration values parsed here
  oidc/
    claims.go        UserClaims{Subject, Email}
    jwks.go          cachingKeySet — JWKS fetch + TTL invalidation
    validator.go     TokenValidator interface + oidcValidator
  allowlist/
    allowlist.go     Checker interface, staticChecker, WithTTLCache wrapper
  token/
    provider.go      Provider interface, FileProvider with RWMutex double-check
  proxy/
    upstream.go      UpstreamProxy (shared by forge, index, weave), SetUserContext
  api/
    handler/
      health.go      /health /livez /readyz
    middleware/
      auth.go        OIDC + allowlist middleware
      requestid.go   X-Request-ID propagation
    router.go        Gin route registration
test/e2e/            End-to-end tests (build tag: e2e)
deployment/          Helm chart
flux/                Flux GitOps manifests (3 environments)
```

---

## Extension points

### Swap the allowlist backend

The `allowlist.Checker` interface has a single method:

```go
type Checker interface {
    Permitted(sub, email string) bool
}
```

To back the allowlist with a database:

1. Implement `Checker` in a new package (e.g. `internal/allowlist/pgchecker`)
2. Wrap it with `allowlist.WithTTLCache(cfg.AllowlistCacheTTL, pgChecker)` in `main.go`
3. No changes needed in the auth middleware or router

The `WithTTLCache` wrapper caches `(sub, email)` results for `ALLOWLIST_CACHE_TTL` (default 30 s), so a permission change takes at most one TTL interval to propagate.

### Add a new upstream service

fusion-weave is an example of this pattern. The general steps:

1. Add `NEWSERVICE_URL` (and optionally `NEWSERVICE_SA_TOKEN_PATH`) to `config.go`
2. Construct `proxy.NewUpstreamProxy(cfg.NewServiceURL, "/api/newservice", saToken)` in `main.go`
3. Register `api.Any("/api/newservice/*path", newServiceProxy.Handler())` in `router.go`

If the upstream validates the SA token via K8s TokenReview (like fusion-weave), use a separate projected volume with no audience restriction for that upstream's token. Add `fusion-platform.io/role: <role>` to the BFF ServiceAccount so the upstream grants the correct RBAC level.

### Replace the OIDC validator

`oidc.TokenValidator` is an interface. A custom implementation (e.g. introspection endpoint, symmetric JWT) can be swapped in `main.go` without touching the auth middleware.

---

## Security model

| Threat | Mitigation |
|---|---|
| Forged OIDC JWT | RS256 signature verified against JWKS; `exp`, `iss`, `aud` all checked |
| Allowlist bypass | `sub` and `email` matching both required; anti-spoofing header deletion |
| Header injection by client | `X-User-ID`, `X-User-Email`, `Authorization` unconditionally deleted before setting |
| SA token exposure | Never returned to client; only used on the server-to-server leg |
| JWKS poisoning | BFF fetches JWKS from the configured URL; clients cannot influence the key set |
| Container breakout | Distroless image, `readOnlyRootFilesystem: true`, runs as non-root (`nonroot` distroless user) |
| SA token audience abuse | forge/index token is audience-scoped to `fusion-bff`; weave token is separate and cannot be used to impersonate the BFF to other services |
| Weave RBAC escalation | BFF SA label `fusion-platform.io/role: admin` is set by Helm; token is never exposed to clients |
