# fusion-bff

Backend for Frontend for the fusion platform GUI.

fusion-bff sits between the Vue.js web GUI and the internal fusion platform services (fusion-forge, fusion-index, fusion-weave). It validates OIDC tokens from human users, enforces an allowlist, resolves RBAC roles and permissions, and forwards requests to backend services using its own Kubernetes service account token with the authenticated user identity passed as a trusted header.

## Purpose

- **OIDC authentication** — validate JWT tokens issued by the configured OIDC provider (Keycloak, Dex, etc.)
- **User allowlist** — reject authenticated users who are not permitted to use the platform
- **RBAC enforcement** — resolve Keycloak groups → roles → permissions; enforce per-route permission checks on all `/api/*` traffic
- **Identity forwarding** — pass the resolved user identity (`X-User-ID`, `X-User-Email`) to upstream services as trusted headers
- **Proxy / routing** — forward API calls to fusion-forge, fusion-index, and fusion-weave; optionally aggregate responses
- **Pod-to-pod traffic is not routed through the BFF** — Kubernetes SA TokenReview auth on forge/index handles that path directly

## Stack

- **Go 1.25**, **Gin** (REST API + reverse proxy); module `github.com/fusion-platform/fusion-bff`
- **OIDC JWT validation** — `github.com/coreos/go-oidc/v3` against JWKS; custom `cachingKeySet` wrapper adds configurable TTL on top of `RemoteKeySet`
- **License**: GPL-3.0

## Logging

Follow `../logging_principles.md` exactly. Key rules:
- No `import "log"` anywhere — use `log/slog` throughout
- `internal/api/middleware/logging.go`: `NewLoggingMiddleware()` + `LoggerFromCtx(c)` — handler errors must use `LoggerFromCtx(c)`, never bare `slog.*`
- `internal/api/handler/helpers.go`: `internalError(c, err)` — use for all unexpected 500 paths (logs + responds)
- **Gin gotcha**: `c.FullPath()` is always `""` before `c.Next()` — capture route template only in the access-log write after `c.Next()` returns
- `setupLogger` reads `LOG_LEVEL`/`LOG_FORMAT` from env directly (not from `cfg`) so it can run before `config.Load()`; X-Request-ID is owned by `NewLoggingMiddleware` — no separate requestid middleware

## Platform context

| Service | Internal URL | Purpose |
|---|---|---|
| fusion-forge | `http://fusion-forge.{namespace}.svc.cluster.local:8080` | Async venv builder |
| fusion-index | `http://fusion-index-backend.{namespace}.svc.cluster.local:8080` | Artifact registry |
| fusion-weave | `http://fusion-weave-api.{namespace}.svc.cluster.local:8082` | Job DAG scheduler (operator API server) |

Namespace pattern: `dev-fusion` / `dev-staging-fusion` / `prod-fusion`

## Auth design

### Human user flow (this service)
```
Browser login (PKCE):
  GET /bff/login → redirect to Keycloak (OIDC_PUBLIC_AUTH_URL, code_challenge S256)
  GET /bff/callback?code=…&state=… → exchange code (OIDC_ISSUER_URL, cluster-internal),
       validate id_token, check allowlist,
       resolve groups → roles → permissions (rbac.Engine),
       store roles+permissions in session → set HttpOnly sid cookie, redirect to /
  POST /bff/logout → revoke refresh token, delete session, clear cookie, redirect to end_session
  GET /bff/userinfo → returns {sub, email, name, roles, permissions} from session

/api/* middleware (APIAuth):
  1. Cookie sid present → load session; silent refresh if access token within 30 s of expiry
     → check RoutePermission(rules, method, path); if required perm not in session.Permissions → 403
  2. No valid cookie → fall back to Bearer <OIDC JWT>
     → resolve groups → permissions on-the-fly; enforce route permission check
  3. Either path → set X-User-ID / X-User-Email on upstream request

Notes:
  - OIDC_ISSUER_URL (cluster-internal) used for token/revoke calls; OIDC_PUBLIC_AUTH_URL for browser redirects
  - Session state is in-memory (InMemoryStore) — single-pod only, no distributed support
  - CookieDomain "auto" derives .parent-domain from Host header for subdomain cookie sharing
  - Keycloak sends groups with a leading "/" (e.g. "/team-data") — validator strips it to bare name
```

### Pod-to-pod flow (existing, not in this service)
```
Pod → Bearer <K8s SA token> → fusion-forge / fusion-index
  TokenReview validates SA directly — no BFF involved
```

## RBAC design

```
Keycloak JWT "groups" claim  (e.g. ["/team-data", "/platform-admin"])
  └─ normalise: strip leading "/"
        │
        ▼
   GroupResolver (internal/rbac/engine.go)
        │  Stage 1: JWTResolver — pass groups through unchanged
        │  Stage 2: DBResolver / MergedResolver (not yet built)
        ▼
   group_roles map  (rbac.yaml)
        │
        ▼
   role_permissions map  (rbac.yaml)
        │
        ▼
   Session { Roles[], Permissions[] }   (stored at login time)
        │
        ├── enforced in APIAuth middleware per route
        └── exposed via GET /bff/userinfo → frontend
```

### rbac.yaml
Loaded from `RBAC_CONFIG_PATH` (default `./rbac.yaml`). In K8s, mount as a ConfigMap volume.
Top-level keys: `group_source` (`jwt`|`db`|`both`), `group_roles`, `role_permissions`, `route_permissions`.

`route_permissions` is an ordered list of `{method, path, permission, resource_type?}` rules — first match wins.
Path patterns: `*` in the middle matches one segment; trailing `*` matches one or more; `<prefix>*` as last segment matches any segment starting with that prefix.
`permission_implies` map: granting a permission also grants listed implied permissions on the same resource (e.g. `index:artifacts:delete` → `index:versions:delete`).
`resource_type` on a rule enables resource-scoped fallback: the first `*` capture in the path is used as `ResourceID` for the `hasResourcePerm()` check in `apiauth.go`.

**`deployment/rbac.yaml` sync**: Helm chart reads from `deployment/rbac.yaml` via `.Files.Get`, NOT from the repo root. Always update BOTH when changing `rbac.yaml`. Deploying with only the root updated silently reverts the configmap to the stale chart copy.

**Helm secrets — two blocks**: `secret.*` (OIDC client secret + session secret) and `db.*` (PostgreSQL DSN). `config.dbDsn` was removed — it landed in the ConfigMap (plaintext). Never use it. DB modes: `db.create=true` + `db.dsn` (chart generates `<release>-db` Secret) or `db.existingSecret=<name>` (ESO/kubectl-managed). Leave both unset when `group_source: jwt`.

**ESO**: External Secrets Operator is deployed in the cluster — prefer `db.existingSecret` for production DB credentials.

**DB pool lifecycle**: Pool is opened whenever `DB_DSN != ""`, regardless of `group_source`. RBAC admin handlers (`adminH`, `resourcePermH`) are still only constructed for `group_source: db/both`. Feature handlers that need DB (e.g. `SystemHealthHandler`) are constructed unconditionally and guard DB calls with `if h.pool != nil` — they degrade gracefully (no overrides) rather than failing at startup when DB is absent.

**Two `/bff/admin` Gin groups**: Router registers two independent groups at `/bff/admin` with different `SessionAuth` permission guards (`admin:roles:manage` for RBAC admin, `admin:health:manage` for health overrides). Gin resolves routes correctly — do not add routes to the wrong group or the wrong permission will be enforced silently.

**Probe error sanitization**: `err.Error()` from `http.Client.Do` contains internal cluster DNS names. Never return it verbatim to clients — use a generic message and `log.Printf` the real error server-side. Always drain the response body (`io.Copy(io.Discard, resp.Body)`) before closing to allow TCP connection reuse.

### Extension points
- **Stage 2 (built)**: `GroupRoleStore` interface in `internal/rbac/store.go` replaces the yaml `group_roles` map at runtime. `StaticGroupRoleStore` (yaml), `DBGroupRoleStore` (postgres), `MergedGroupRoleStore` (union). Switch `group_source: db` or `both` in `rbac.yaml`. Requires `DB_DSN`. Admin API at `GET/POST/DELETE /bff/admin/group-roles` (requires `admin:roles:manage`).
- **`group_source: db` bootstrap gotcha**: DB is empty on first deploy — nobody has admin role to seed it. Use `group_source: both` (yaml as baseline + DB extras) or manually `INSERT INTO group_role_assignments` via `kubectl exec` on the postgres pod.
- **Stage 3 (built)**: `resource_permissions` table in DB; `ResolveResourcePermissions()` in engine; `ResourcePermissions []ResourcePermission` on session; `MatchRoute()` replaces `RoutePermission()` (captures first `*` as ResourceID); `ResourcePermHandler` at `/bff/admin/resource-permissions`; `GET /bff/admin/rbac-config` for dropdown data.
- **Resource permissions are session-bound**: `ResolveResourcePermissions` runs at login (Callback handler). Grant/revoke changes take effect only after the affected user re-logs in.

### Adding weave action sub-paths (e.g., /stop, /retry)
New action endpoints under existing weave resource paths need only a `route_permissions` entry in `rbac.yaml` (+ sync to `deployment/rbac.yaml`). The `api.Any("/weave/*path", weave.Handler())` wildcard proxy already forwards any HTTP method, stripping the `/api/weave` prefix. No router or Go code changes required.
`weave:steps:restart` is the run-state-mutation permission (used for PATCH and action sub-paths like `/stop`). The name is historical; it covers any write that changes run phase.
**Route ordering**: trailing `*` matches one-or-more segments, so `runs/*` matches both `runs/{name}` and `runs/{name}/stop`. For POST action rules, place the specific sub-path rule (`runs/*/stop`) before any broader POST rules to guarantee first-match wins.

## Allowlist design

`ALLOWED_USERS` is comma-separated. Entries containing `@` match the JWT `email` claim; all other entries match `sub`. Empty = allow any authenticated user.

The `internal/allowlist` package exposes a `Checker` interface — swap in a DB-backed implementation and wrap with `allowlist.WithTTLCache(ttl, inner)` for cached I/O. The static in-memory checker (default) should NOT be wrapped in the TTL cache.

## What fusion-forge / fusion-index need (future changes, not yet done)

- Accept `X-User-ID` trusted header when the calling SA is the BFF service account
- Populate `creator_id` from that header instead of the SA username
- Scope `List` endpoints to `creatorId = caller` for non-admin SAs

## Deployment

Same Flux + Helm pattern as fusion-forge:
- Helm chart at `deployment/`
- Flux config at `flux/` with three environments: `dev-fusion`, `dev-staging-fusion`, `prod-fusion`
- Self-contained chart (no Bitnami or external subchart dependencies)

## Key environment variables

| Variable | Default | Description |
|---|---|---|
| `POST_LOGIN_REDIRECT_URL` | `/` | Where the browser is sent after a successful login (`/bff/callback`) |
| `HTTP_PORT` | `8080` | Listen port |
| `OIDC_ISSUER_URL` | — | OIDC provider issuer URL |
| `OIDC_CLIENT_ID` | — | Expected `aud` claim value |
| `OIDC_JWKS_URL` | `{OIDC_ISSUER_URL}/protocol/openid-connect/certs` | Override JWKS endpoint (required for non-Keycloak providers) |
| `OIDC_JWKS_CACHE_TTL` | `15m` | How often to force-refresh the JWKS key set |
| `ALLOWED_USERS` | — | Comma-separated `sub` or `email` values; empty = any authenticated user |
| `FORGE_URL` | `http://fusion-forge.fusion.svc.cluster.local:8080` | fusion-forge base URL |
| `INDEX_URL` | `http://fusion-index-backend.fusion.svc.cluster.local:8080` | fusion-index base URL |
| `WEAVE_URL` | `http://fusion-weave-api.fusion.svc.cluster.local:8082` | fusion-weave API server base URL |
| `FORGE_HEALTH_URL` | `{FORGE_URL}/health` | Health probe URL for forge (override if path differs) |
| `INDEX_HEALTH_URL` | `{INDEX_URL}/health` | Health probe URL for index |
| `WEAVE_HEALTH_URL` | `{WEAVE_URL}/health` | Health probe URL for weave |
| `HEALTH_PROBE_TIMEOUT` | `5s` | Per-probe HTTP timeout for upstream health checks |
| `K8S_SA_TOKEN_PATH` | `/var/run/secrets/kubernetes.io/serviceaccount/token` | SA token for forge/index calls (audience: fusion-bff) |
| `WEAVE_SA_TOKEN_PATH` | `/var/run/secrets/fusion-bff/weave/token` | SA token for weave calls (no audience restriction; required for K8s TokenReview) |
| `SA_TOKEN_CACHE_TTL` | `5m` | How long to cache SA tokens before re-reading from disk (applies to both paths) |
| `ALLOWLIST_CACHE_TTL` | `30s` | TTL for `WithTTLCache` wrapper (only used with DB-backed Checker) |
| `OIDC_PUBLIC_AUTH_URL` | — | Browser-visible Keycloak base URL for auth redirects (may differ from OIDC_ISSUER_URL) |
| `OIDC_CLIENT_SECRET` | — | Client secret for authorization_code exchange |
| `OIDC_REDIRECT_URL` | — | Callback URL registered in Keycloak (e.g. `https://bff.fusion.local/bff/callback`) |
| `OIDC_REVOKE_URL` | `{OIDC_ISSUER_URL}/protocol/openid-connect/revoke` | Token revocation endpoint |
| `OIDC_END_SESSION_URL` | `{OIDC_ISSUER_URL}/protocol/openid-connect/logout` | Keycloak end_session (browser redirect on logout) |
| `SESSION_COOKIE_NAME` | `sid` | Session cookie name |
| `SESSION_COOKIE_DOMAIN` | `""` | Cookie Domain attr: `""` = omit, `"auto"` = derive `.parent` from Host, or literal value |
| `SESSION_COOKIE_SECURE` | `false` | Set Secure flag on session cookie; set `true` in production |
| `SESSION_MAX_AGE` | `8h` | Maximum session lifetime (also sets cookie MaxAge) |
| `OIDC_BYPASS` | `false` | `true` = start embedded mock OIDC server; skips real OIDC validation and allowlist — **never use in production** |
| `OIDC_BYPASS_BASE_URL` | `http://localhost:{HTTP_PORT}` | Browser-visible BFF base URL used to build mock OIDC redirect URLs |
| `OIDC_BYPASS_SUB` | `dev-user` | Subject (`sub`) claim pre-filled in the mock login form |
| `OIDC_BYPASS_EMAIL` | `dev@local` | Email claim pre-filled in the mock login form |
| `OIDC_BYPASS_NAME` | `Dev User` | Display name claim pre-filled in the mock login form |
| `OIDC_BYPASS_GROUPS` | `""` | Comma-separated group names pre-selected in the mock login form's group selector |
| `RBAC_CONFIG_PATH` | `./rbac.yaml` | Path to the RBAC config file; mount via ConfigMap in K8s |
| `DB_DSN` | — | PostgreSQL DSN; required when `rbac.yaml` `group_source` is `db` or `both` |

## Local dev (minikube)
- Always use semver image tags (`fusion-bff:X.Y.Z`) — never `latest` or `local`; bump on each deploy
- Build inside minikube daemon: `eval $(minikube docker-env) && docker build -t fusion-bff:X.Y.Z .`
- Dev Vite server: `POST_LOGIN_REDIRECT_URL=http://dev.fusion.local:5174`, `CORS_ORIGINS=http://dev.fusion.local:5174`
- In-cluster spectra (bypass mode): `POST_LOGIN_REDIRECT_URL=http://spectra.fusion.local/`, `CORS_ORIGINS=http://spectra.fusion.local`, `SESSION_COOKIE_DOMAIN=auto` — see DEV.md for complete Helm commands
- Set `OIDC_BYPASS_GROUPS=platform-admin` (or comma-separated list) to pre-select groups on the mock login form
- Upstream proxy (`internal/proxy/upstream.go`) strips CORS headers in `ModifyResponse` — prevents duplicate `Access-Control-Allow-Origin` when upstream also sets it
- **Helm chart pre-flight for RBAC**: chart needs `OIDC_BYPASS_GROUPS` + `RBAC_CONFIG_PATH` in `configmap.yaml`, and a ConfigMap volume mounting `rbac.yaml` into the pod — see `TEST_PLAN_session1.md` section 0 for exact snippets

## Minikube testing gotchas

- **OIDC issuer mismatch**: BFF rejects tokens fetched via `localhost` port-forward — the `iss` claim won't match `OIDC_ISSUER_URL`. Fetch tokens from inside the cluster: `kubectl run token-fetch --rm -i --restart=Never --image=alpine/curl:latest --namespace fusion -- sh -c 'curl -s -X POST http://keycloak.default.svc.cluster.local:8080/realms/fusion/protocol/openid-connect/token -d "grant_type=password&client_id=fusion-gui&client_secret=<secret>&username=testuser&password=password"' 2>/dev/null | grep -o '"access_token":"[^"]*"' | cut -d'"' -f4`
- **Keycloak `fusion-gui` is a confidential client** — always pass `client_secret` when fetching tokens; omitting it returns `unauthorized_client`
- **Helm field manager conflicts**: Resources with `kubectl-patch` ownership block `helm upgrade` with "conflict with kubectl". Fix: `helm get values <release> -n <ns> -o yaml > /tmp/vals.yaml && helm template <release> <chart> -f /tmp/vals.yaml -n <ns> | kubectl apply --server-side --force-conflicts -n <ns> -f -`, then re-run `helm upgrade`. Steal ownership once; subsequent upgrades work normally.
- **ConfigMap env vars**: Updating a ConfigMap doesn't restart pods — run `kubectl rollout restart deployment/<name> -n <namespace>` to pick up changes

## Commands

```bash
# Dev build
go build ./...

# Unit tests
go test ./... -v -race

# e2e tests (no external services needed — uses httptest mock servers)
go test ./test/e2e/... -tags e2e -v -timeout=120s

# Build Docker image (inside minikube)
make docker-build IMG=fusion-bff:local

# Run locally (reads .env if present)
make run

# Port-forward
kubectl port-forward -n fusion service/fusion-bff 18081:8080 --address 127.0.0.1
```

## Project structure

```
cmd/
  server/main.go           # Entry point — loads rbac.yaml, builds Engine, wires everything
internal/
  config/config.go         # Env var loading — includes OIDCBypassGroups, RBACConfigPath
  allowlist/allowlist.go   # Checker interface + static impl + WithTTLCache wrapper
  rbac/
    config.go              # RBACConfig, RouteRule (+ ResourceType), PermissionImplies, LoadConfig, GroupNames()
    engine.go              # Engine.Resolve() + ResolveResourcePermissions() + RBACConfigSummary()
    route.go               # MatchRoute() + matchAndCapture() — first-match glob with ResourceID capture; RoutePermission() is a thin wrapper
    route_capture_test.go  # unit tests for MatchRoute capture semantics
    store.go               # GroupRoleStore interface
    static_store.go        # StaticGroupRoleStore — yaml group_roles map
    db_store.go            # DBGroupRoleStore — postgres-backed
    merged_store.go        # MergedGroupRoleStore — union of static + db
  db/
    db.go                  # Open(), Migrate() — idempotent CREATE TABLE IF NOT EXISTS; includes resource_permissions + service_status_overrides tables
    queries.go             # ListGroupRoles, CreateGroupRole, DeleteGroupRole, LoadAllGroupRoles; + ListResourcePerms, CreateResourcePerm, DeleteResourcePerm, LoadResourcePermsForUser; + ListServiceStatuses, UpsertServiceStatus, DeleteServiceStatus
  oidc/
    claims.go              # UserClaims { Subject, Email, Name, Groups }
    validator.go           # JWT validation; extracts + normalises "groups" claim
    jwks.go                # JWKS fetching and caching (cachingKeySet with TTL)
  mockoidc/                # Embedded mock OIDC — active only when OIDC_BYPASS=true
    server.go              # RSA key gen; login form with group multi-select; groups in JWT
    validator.go           # mockValidator — verifies JWTs; extracts Groups claim
  token/provider.go        # SA token Provider interface + FileProvider (TTL cache)
  proxy/
    upstream.go            # UpstreamProxy (single type used for forge, index, and weave)
  session/
    session.go             # Session { …, Roles []string, Permissions []string, ResourcePermissions []ResourcePermission }
  api/
    handler/health.go      # /health /livez /readyz
    handler/auth.go        # /bff/login, /bff/callback (resolves RBAC + resource perms), /bff/logout, /bff/userinfo
    handler/admin.go       # /bff/admin/group-roles (GET/POST/DELETE); GET /bff/admin/rbac-config
    handler/resource_permissions.go  # GET/POST/DELETE /bff/admin/resource-permissions (Stage 3)
    handler/system_health.go  # GET /bff/system-health (all users); GET/PUT/DELETE /bff/admin/service-status (admin:health:manage)
    middleware/auth.go     # OIDC Bearer middleware — validate + allowlist + set user context
    middleware/apiauth.go  # /api/* combined middleware: session cookie + Bearer fallback + RBAC enforcement
    middleware/session_auth.go  # SessionAuth — cookie-only auth + permission check for /bff/admin/*
    middleware/cors.go     # CORS middleware
    middleware/requestid.go
    router.go              # Gin routes
rbac.yaml                  # Root config — keep in sync with deployment/rbac.yaml (Helm chart reads the latter)
deployment/rbac.yaml       # Helm chart copy — updated by `cp rbac.yaml deployment/rbac.yaml` before deploying
test/e2e/                  # e2e tests (build tag: e2e); uses httptest mock OIDC + upstreams
deployment/                # Helm chart
flux/                      # Flux GitOps (3 environments)
Dockerfile
Makefile
```

## Changelog rule

**Every bugfix and every feature must be logged in `CHANGELOG.md` before the commit is made.**

- Follow the [Keep a Changelog](https://keepachangelog.com/en/1.0.0/) format; `../fusion-spectra/CHANGELOG.md` is the sibling project reference for style and granularity.
- Add a new `## [x.y.z] — YYYY-MM-DD` section at the top (below `[Unreleased]`); bump the patch version for fixes, minor version for new features.
- Use `### Added`, `### Changed`, `### Fixed`, or `### Removed` subsections as appropriate.
- One bullet per logical change; keep it concise but self-contained (reader should not need to read the diff).
- Also sync `deployment/rbac.yaml` and bump `deployment/Chart.yaml` `version`/`appVersion` when releasing.
