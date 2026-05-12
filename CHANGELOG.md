# Changelog

All notable changes to fusion-bff are documented here.
Format: [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

---

## [Unreleased]

### Added
- fusion-content proxy: `GET /api/content/*` routes to the changelog aggregation service via SA token auth
- `CONTENT_URL` env var (default `http://fusion-content.fusion.svc.cluster.local:8080`) and `CONTENT_HEALTH_URL` (default `{CONTENT_URL}/q/health/ready`)
- `content:changelog:read` permission added to `admin`, `engineer`, and `viewer` roles in `rbac.yaml`
- Route permission rule: `GET /api/content/*` → `content:changelog:read`
- `content` added to system health probe targets (`GET /bff/system-health`) and to the `validServices` allowlist for status overrides

---

## [0.3.1] — 2026-05-07

### Added
- System health API: `GET /bff/system-health` (all authenticated users) — probes fusion-forge, fusion-index, and fusion-weave; returns per-service `healthy/unhealthy/offline/maintenance` status
- Admin service status override API: `GET/PUT/DELETE /bff/admin/service-status` — gated by `admin:health:manage` permission
- `service_status_overrides` table in DB (`db.Migrate`); `ListServiceStatuses`, `UpsertServiceStatus`, `DeleteServiceStatus` queries
- `HEALTH_PROBE_TIMEOUT`, `FORGE_HEALTH_URL`, `INDEX_HEALTH_URL`, `WEAVE_HEALTH_URL` env vars for configurable upstream health endpoints
- `admin:health:manage` permission added to `platform-admin` role in `rbac.yaml`
- Dedicated `/bff/admin` Gin group with `SessionAuth(admin:health:manage)` guard for health-override routes (separate from RBAC admin group)
- `SystemHealthHandler` degrades gracefully when DB pool is absent (`group_source: jwt`)

### Fixed
- Probe error sanitization: `err.Error()` from `http.Client.Do` is logged server-side only; clients receive a generic message to prevent leaking internal cluster DNS names

---

## [0.3.0] — 2026-05-06

### Fixed
- Missing RBAC route rules for forge git build endpoints: added `POST /api/forge/api/v1/gitbuilds/validate` and `POST /api/forge/api/v1/gitbuilds` (permission: `forge:builds:create`) — these returned 403 without them

---

## [0.2.2] — 2026-04-29

### Added
- Weave chain and trigger RBAC permissions: `weave:chains:write`, `weave:chains:delete`, `weave:triggers:write`, `weave:triggers:delete` for `platform-admin` and `engineer` roles
- Route permission rules for `/api/weave/api/v1/chains` and `/api/weave/api/v1/triggers` (GET/POST/PUT/PATCH/DELETE)

---

## [0.2.1] — 2026-04-28

### Added
- Weave job-template and service-template RBAC permissions: `weave:jobtemplates:write/delete`, `weave:servicetemplates:write/delete` for `platform-admin`; write variants for `engineer`
- `weave:resources:read` added to `platform-admin`, `engineer`, and `viewer` roles
- Route permission rules for `/api/weave/api/v1/jobtemplates` and `/api/weave/api/v1/servicetemplates`

---

## [0.2.0] — 2026-04-28

### Changed
- Helm secret layout split into two blocks: `secret.*` (OIDC client secret + session secret) and `db.*` (PostgreSQL DSN) — `config.dbDsn` removed from ConfigMap (plaintext leak)
- `db.create=true` + `db.dsn` mode: chart generates `<release>-db` Secret; `db.existingSecret` mode for ESO/kubectl-managed credentials

---

## [0.1.0] — 2026-04-27

### Added
- RBAC Stage 2 — DB-backed group→role assignments: `GroupRoleStore` interface (`store.go`); `StaticGroupRoleStore`, `DBGroupRoleStore`, `MergedGroupRoleStore` implementations
- RBAC Stage 3 — resource-scoped permissions: `resource_permissions` DB table; `ResolveResourcePermissions()` in engine; `MatchRoute()` captures first `*` as `ResourceID`; `ResourcePermHandler` at `/bff/admin/resource-permissions`
- RBAC admin API: `GET/POST/DELETE /bff/admin/group-roles` (requires `admin:roles:manage`)
- `GET /bff/admin/rbac-config` — returns groups, roles, and permissions for admin UI dropdowns
- DB layer: `db.Open()` + `db.Migrate()` (idempotent `CREATE TABLE IF NOT EXISTS`); `group_role_assignments` + `resource_permissions` + `service_status_overrides` tables
- `SessionAuth` middleware for `/bff/admin/*` routes (cookie-only, permission-checked)
- `group_source` config key (`jwt` | `db` | `both`) in `rbac.yaml`
- `RBAC_CONFIG_PATH`, `DB_DSN` env vars
- `permission_implies` map in `rbac.yaml` (e.g. `index:artifacts:delete` → `index:versions:delete`)
- `resource_type` on route rules enables resource-scoped fallback in `apiauth.go`
- Mock OIDC server: group multi-select in login form; `OIDC_BYPASS_GROUPS` env var pre-populates selection
- e2e test suite expanded with RBAC + admin route coverage

### Changed
- `RoutePermission()` replaced by `MatchRoute()` (backwards-compatible thin wrapper retained)
- Session extended: `ResourcePermissions []ResourcePermission` field added
- OIDC validator normalises Keycloak groups with leading `/`
- `APIAuth` middleware: session cookie path + Bearer fallback path both enforce `MatchRoute` permission check

---

## [0.0.2] — 2026-04-23

### Added
- Mock OIDC server (`internal/mockoidc`): RSA key gen, login form, JWT issuance — active only when `OIDC_BYPASS=true`
- `OIDC_BYPASS`, `OIDC_BYPASS_BASE_URL`, `OIDC_BYPASS_SUB`, `OIDC_BYPASS_EMAIL`, `OIDC_BYPASS_NAME` env vars
- `SESSION_COOKIE_DOMAIN` `"auto"` mode: derives `.parent-domain` from Host header
- `OIDC_PUBLIC_AUTH_URL` env var: separate browser-visible Keycloak URL for auth redirects
- JWKS caching wrapper (`cachingKeySet`) with configurable `OIDC_JWKS_CACHE_TTL`
- SA token file caching with `SA_TOKEN_CACHE_TTL`
- DEV.md and EXAMPLE.md added with local dev + minikube instructions

---

## [0.0.1] — 2026-04-23

### Added
- Project scaffold: Go 1.25 + Gin; module `github.com/fusion-platform/fusion-bff`
- OIDC JWT validation (`internal/oidc`): JWKS fetch + `go-oidc/v3`, `UserClaims` extraction
- User allowlist (`internal/allowlist`): `Checker` interface, static impl, `WithTTLCache` wrapper
- Session management (`internal/session`): `InMemoryStore`, `Session` type with `Roles`, `Permissions`
- OIDC login flow: `GET /bff/login`, `GET /bff/callback`, `POST /bff/logout`, `GET /bff/userinfo`
- `APIAuth` middleware: session cookie auth + Bearer fallback + per-route RBAC enforcement
- `OIDC` middleware for direct Bearer token paths
- Reverse proxy (`internal/proxy/upstream.go`): single `UpstreamProxy` type for forge, index, and weave; strips duplicate CORS headers in `ModifyResponse`
- SA token provider (`internal/token`): `FileProvider` with TTL cache
- Health endpoints: `/health`, `/livez`, `/readyz`
- CORS middleware, request-ID middleware
- Helm chart at `deployment/` with Flux GitOps config for three environments (`dev-fusion`, `dev-staging-fusion`, `prod-fusion`)
- Dockerfile, Makefile
- e2e test suite (`test/e2e/`) using `httptest` mock OIDC + upstream servers
