# Architecture

## Overview

`fusion-bff` is a reverse proxy that adds browser-safe authentication and RBAC enforcement between the Vue GUI and the internal platform services. Its responsibilities:

1. Run the OIDC PKCE login flow and maintain server-side sessions (HttpOnly cookie)
2. Resolve user identity to roles and permissions via the RBAC engine
3. Enforce route-level permission checks on all `/api/*` requests
4. Replace the inbound session cookie / Bearer token with the BFF's own K8s service account token
5. Forward the resolved user identity as trusted headers
6. Expose an admin API for managing group→role assignments and resource-scoped permission grants

---

## Component map

```
┌──────────────────────────────────────────────────────────────────┐
│  fusion-bff                                                      │
│                                                                  │
│  cmd/server/main.go                                              │
│    └─ config.Load()              reads env vars                  │
│    └─ rbac.LoadConfig()          loads rbac.yaml                 │
│    └─ if OIDCBypass:                                             │
│         mockoidc.New()           RSA key + mock server           │
│         mockoidc.Validator()     in-process JWT verifier         │
│         allowlist.New(nil)       allow-all checker               │
│       else:                                                      │
│         oidc.NewValidator()      JWKS-backed JWT verifier        │
│         allowlist.New()          in-memory sub/email checker     │
│    └─ db.Open() + db.Migrate()   if group_source != jwt          │
│    └─ rbac.NewEngine(cfg, pool)  picks GroupRoleStore            │
│    └─ token.NewFileProvider() ×2 (saToken, weaveSAToken)        │
│    └─ proxy.NewUpstreamProxy() ×3  (forge, index, weave)        │
│    └─ api.NewRouter()            gin engine                      │
│    └─ http.Server.ListenAndServe()                               │
│                                                                  │
│  internal/api/router.go                                          │
│    /health  /livez  /readyz     → handler.Health (no auth)      │
│    /bff/login|callback|logout   → handler.Auth (PKCE flow)      │
│    /bff/userinfo                → handler.Auth (session read)   │
│    /bff/admin/*                 → middleware.RequirePermission   │
│                                   + handler.Admin               │
│    /api/forge/*path             → middleware.APIAuth → forge     │
│    /api/index/*path             → middleware.APIAuth → index     │
│    /api/weave/*path             → middleware.APIAuth → weave     │
│                                                                  │
│  internal/api/middleware/apiauth.go                              │
│    1. sid cookie present? → session.Store.Get() → Session        │
│       (refresh access token if within 30 s of expiry)           │
│    2. No session? → fall back to Bearer <OIDC JWT>              │
│    3. rbac.RoutePermission(rules, method, path)                  │
│       → 403 if required permission not in session.Permissions   │
│    4. proxy.SetUserContext(r, sub, email)                        │
│                                                                  │
│  internal/rbac/engine.go                                         │
│    Resolve(ctx, sub, jwtGroups)                                  │
│      └─ GroupRoleStore.RolesForGroup() per group                 │
│           StaticGroupRoleStore  ← rbac.yaml (group_source: jwt) │
│           DBGroupRoleStore      ← postgres  (group_source: db)  │
│           MergedGroupRoleStore  ← both      (group_source: both)│
│      └─ cfg.RolePermissions[role] → flatten to permissions      │
│      └─ session.Roles, session.Permissions populated            │
│                                                                  │
│  internal/proxy/upstream.go                                      │
│    Handler()  pre-fetches SA token, stores in ctx               │
│    Rewrite()  strips /api/{forge|index|weave} prefix            │
│               deletes inbound X-User-ID / X-User-Email          │
│               sets Authorization: Bearer <SA token>             │
│               sets X-User-ID / X-User-Email from ctx            │
└──────────────────────────────────────────────────────────────────┘
          │               │               │
          ▼               ▼               ▼
  fusion-forge:8080  fusion-index-  fusion-weave-api:8082
```

---

## Request lifecycle — browser session

```
GUI
 │  GET /api/index/api/v1/artifacts
 │  Cookie: sid=<session-id>
 │
 ▼
middleware.APIAuth
 ├─ session.Store.Get(sid)        → Session{ Sub, Roles, Permissions, ... }
 │    └─ token refresh if near expiry
 ├─ rbac.RoutePermission(rules, GET, /api/index/.../artifacts)
 │    → required: "index:artifacts:read"
 ├─ "index:artifacts:read" ∈ session.Permissions?
 │    yes → continue   /   no → 403
 └─ proxy.SetUserContext(r, sub, email)
 │
 ▼
proxy.UpstreamProxy.Handler()
 ├─ token.FileProvider.Token()    (disk read, cached)
 └─ httputil.ReverseProxy.ServeHTTP()
      └─ Rewrite:
           strip /api/index prefix  →  /api/v1/artifacts
           del X-User-ID, X-User-Email, Authorization
           set Authorization: Bearer <SA token>
           set X-User-ID: <sub>
           set X-User-Email: <email>
 │
 ▼
fusion-index-backend  GET /api/v1/artifacts
```

---

## RBAC model

```
Keycloak groups (JWT "groups" claim)
      │
      ▼
GroupRoleStore.RolesForGroup(group)
      │  StaticGroupRoleStore  ← rbac.yaml group_roles map
      │  DBGroupRoleStore      ← group_role_assignments table
      │  MergedGroupRoleStore  ← union of both
      ▼
Roles[]  →  cfg.RolePermissions[role]  →  Permissions[]
      │
      ├── stored in session on login / token refresh
      └── returned to frontend via GET /bff/userinfo
                { sub, email, name, roles, permissions, resource_permissions }
```

### Resource-scoped permissions

In addition to global permissions, the BFF stores per-resource grants in `resource_permissions` table:

```sql
subject_type TEXT  -- "user" | "group" | "role"
subject      TEXT  -- identity name / OIDC sub
permission   TEXT  -- e.g. "index:artifacts:delete"
resource_type TEXT -- "artifact" | "venv"
resource_id  TEXT  -- the resource's numeric ID as string
```

These are resolved at login and embedded in the `userinfo` response as `resource_permissions[]`. The frontend's `usePermission().can(permission, resourceId)` checks global permissions first, then falls back to the resource-scoped list.

---

## Permission strings

```
index:artifacts:read      index:artifacts:write    index:artifacts:delete
index:versions:write      index:versions:delete    index:types:manage
forge:builds:read         forge:builds:create
admin:users:view          admin:roles:manage
```

---

## Package structure

```
cmd/server/          Entry point — wires all components and starts the HTTP server
internal/
  config/            Env var loading; all time.Duration values parsed here
  oidc/
    claims.go        UserClaims{Subject, Email, Name, Groups}
    jwks.go          cachingKeySet — JWKS fetch + TTL invalidation
    validator.go     TokenValidator interface + oidcValidator (production)
  mockoidc/          Embedded mock OIDC — active only when OIDC_BYPASS=true
    server.go        RSA keypair, login form with group selector, auth code store
    validator.go     mockValidator — verifies JWTs using the in-memory key
  allowlist/
    allowlist.go     Checker interface, staticChecker, WithTTLCache wrapper
  token/
    provider.go      Provider interface, FileProvider with RWMutex double-check
  proxy/
    upstream.go      UpstreamProxy (shared by forge, index, weave), SetUserContext
  session/
    session.go       Session{Sub,Email,Name,Roles,Permissions,ResourcePermissions}, InMemoryStore
  rbac/
    config.go        RBACConfig, RouteRule, LoadConfig
    engine.go        Engine — resolves groups → roles → permissions
    store.go         GroupRoleStore interface
    static_store.go  StaticGroupRoleStore (rbac.yaml)
    db_store.go      DBGroupRoleStore (postgres)
    merged_store.go  MergedGroupRoleStore (both)
    route.go         RoutePermission — first-match rule evaluation
  db/
    db.go            Open + Migrate (group_role_assignments, resource_permissions tables)
    queries.go       CRUD for both tables + LoadAllGroupRoles
  api/
    handler/
      health.go      /health /livez /readyz
      auth.go        /bff/login, /bff/callback, /bff/logout, /bff/userinfo
      admin.go       /bff/admin/group-roles, /bff/admin/resource-permissions, /bff/admin/rbac-config
    middleware/
      apiauth.go     /api/* — session cookie + Bearer fallback + route permission check
      cors.go        CORS middleware
      requestid.go   X-Request-ID propagation
    router.go        Gin route registration
test/e2e/            End-to-end tests (build tag: e2e)
deployment/          Helm chart
flux/                Flux GitOps manifests (3 environments)
```

---

## Key design decisions

### SA token pre-fetched before proxying

`httputil.ReverseProxy.Rewrite` cannot return an error. To handle SA token fetch failures cleanly, the token is fetched in `Handler()` before `rp.ServeHTTP` is called. On failure the handler returns `502 upstream unavailable` immediately.

### Separate SA token per upstream (audience isolation)

| Volume | Audience | Used for |
|---|---|---|
| `sa-token` | `fusion-bff` | forge, index |
| `weave-sa-token` | *(none)* | fusion-weave (TokenReview validates against kube-apiserver) |

A projected token with `audience: fusion-bff` fails Kubernetes TokenReview — the API server validates against its own audiences. Omitting the audience makes it valid for TokenReview.

### User identity via `context.Context`, not Gin context

The proxy `Rewrite` function receives `*httputil.ProxyRequest`, not a Gin context. Passing identity through `context.WithValue` avoids importing Gin into the `proxy` package.

### Anti-spoofing header deletion

`Rewrite` unconditionally deletes `X-User-ID`, `X-User-Email`, and `Authorization` before setting them from the validated context. A malicious client cannot inject trusted identity headers.

### Resource permissions at login, not per-request

Resource-scoped grants are small enough to embed in the `userinfo` response at login. No per-request DB lookup is needed in components — the frontend checks `auth.user.resource_permissions` locally.

---

## Security model

| Threat | Mitigation |
|---|---|
| Forged OIDC JWT | RS256 signature verified against JWKS; `exp`, `iss`, `aud` all checked |
| Allowlist bypass | `sub` and `email` matching; anti-spoofing header deletion |
| Header injection by client | `X-User-ID`, `X-User-Email`, `Authorization` unconditionally deleted before setting |
| SA token exposure | Never returned to client; only used on the server-to-server leg |
| Privilege escalation via route | `rbac.RoutePermission` enforced on every `/api/*` request; 403 if permission missing |
| RBAC bypass via session | Roles/permissions stored in server-side session — client cannot modify them |
| OIDC bypass misuse | `OIDC_BYPASS=true` prints a loud `[WARNING]` at startup; guard with Helm `required` in prod |
| DB credentials in config | `DB_DSN` injected from a K8s Secret (chart-generated or ESO-managed); never stored in ConfigMap or `values.yaml` |
| Container breakout | Distroless image, `readOnlyRootFilesystem: true`, runs as non-root |
