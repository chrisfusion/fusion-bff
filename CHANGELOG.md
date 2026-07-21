# Changelog

All notable changes to fusion-bff are documented here.
Format: [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

---

## [Unreleased]

## [0.8.1] — 2026-07-21

### Changed
- **Breaking (docs only):** Updated `internal/docs/openapi.yaml` for fusion-weave's ingress hostname rework — `WeaveIngressRule.host` is now `WeaveIngressRule.name` and `WeaveRunStepOverride.ingressHost` is now `WeaveRunStepOverride.ingressName`, both DNS-label-only (`maxLength: 63`, `pattern: ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`). The operator now appends a cluster-wide `ingress.hostSuffix` to build the real hostname, so templates/runs can no longer specify an arbitrary external domain. Pure passthrough proxy — no BFF Go code, RBAC rule, or route change needed since the resource types were already covered by existing `weave:*` route rules.

## [0.8.0] — 2026-07-13

### Added
- `GET /bff/presets` — serves this unit's static infrastructure presets (Kafka broker clusters, secret names) so fusion-spectra creation wizards can offer a dropdown instead of requiring the exact resource name to be typed. New `internal/presets` package loads an optional `presets.yaml` (missing file yields an empty preset set, unlike the mandatory `rbac.yaml`), mounted via a new `<release>-presets` ConfigMap at `/etc/fusion-bff/presets/presets.yaml` — edited the same way as `rbac.yaml` (patch the ConfigMap + roll the deployment) so each unit curates its own presets independently. Gated by new `bff:presets:read` permission (granted to `admin`/`engineer`, checked directly via `SessionAuth` — no `route_permissions` rule needed since this isn't a proxied `/api/*` route).

## [0.7.2] — 2026-07-13

### Added
- Documented `codeSource` (`WeaveJobTemplateSpec`) in `internal/docs/openapi.yaml`, reflecting the new fusion-weave feature letting Job-kind steps reference a versioned fusion-index artifact via the same `code-loader` init container mechanism as Deploy-kind steps' `WeaveServiceTemplateSpec.codeSource` — reuses the existing `WeaveCodeSourceSpec` schema. No RBAC or route changes needed: field-only addition on a resource already covered by the existing `weave:jobtemplates:write`/`weave:jobtemplates:delete` route rules.

## [0.7.1] — 2026-07-13

### Added
- Documented `authSecretRef` (`WeaveChainSpec`) and `authSecretRefOverride` (`WeaveTriggerSpec`, `WeaveRunSpec`) in `internal/docs/openapi.yaml`, reflecting the new fusion-weave feature that injects a Secret via `envFrom` into every step pod so runner-side helper libraries (e.g. the `fusion-runner` Python `KeycloakAuth` helper) can read credential keys as env vars. No RBAC or route changes needed — these are new fields on existing chain/trigger/run resources, already covered by the existing `weave:chains:write`/`weave:triggers:write`/`weave:runs:write` route rules and the generic `/weave/*path` proxy.

## [0.7.0] — 2026-07-07

### Added
- `postgresql.createDatabaseJob` — a Helm `pre-install,pre-upgrade` hook Job (`deployment/templates/postgresql-create-db-job.yaml`) that creates the fusion-bff database on an existing/shared PostgreSQL server before the main Deployment is applied. Idempotent (`SELECT ... pg_database` check before `CREATE DATABASE`), using `psql` in a `postgres:16-alpine` container running as UID 70 non-root
- `postgresql.external.*` values (`host`, `port`, `username`, `existingSecret`/`password`) — mirrors the `postgresql.auth`/`postgresql.external` convention already used by fusion-forge and fusion-index's charts, with a Secret key of `"password"`. This is a separate credential/secret from the app's own `db.*` runtime `DB_DSN`, since creating a database requires connecting with admin credentials before that database — and therefore that DSN — exists
- `deployment/templates/postgresql-admin-secret.yaml` — chart-managed Secret for the admin credentials, only rendered when `postgresql.external.existingSecret` is unset
- Does **not** run schema migrations or seed data — table creation stays in the app binary's own `Migrate()` step on startup, per existing design
- `rbacSeed` — a second Helm `pre-install,pre-upgrade` hook Job (`deployment/templates/rbac-seed-job.yaml`, weight `-3`, runs after `postgresql.createDatabaseJob` and before the main Deployment) that ensures one group→role admin mapping exists in `group_role_assignments`, so `group_source: db` doesn't start with an empty table and no path to create the first admin. Connects using the app's own `db.*` `DB_DSN` (not the create-db job's admin credentials). Only configurable value is `rbacSeed.adminGroup` (default `platform-admin`) — an OIDC/Keycloak group name, since roles are always resolved from the JWT `groups` claim rather than a specific user id. Verified end-to-end against a real `postgres:16-alpine` container, including idempotent re-run

## [0.6.1] — 2026-07-07

### Fixed
- Closed two RBAC gaps found while documenting the proxy surface in 0.6.0, where a route matched no `route_permissions` rule and was reachable by any authenticated caller:
  - `PUT /api/index/api/v1/artifacts/*` now requires `index:artifacts:write` — covers both `PUT .../artifacts/{id}` (update description) and `PUT .../artifacts/{id}/types/{typeId}` (assign type), since the trailing wildcard matches both depths
  - `PUT /api/weave/api/v1/runs/*` now requires `weave:runs:write`, matching the permission already required for `POST /runs` (create)
- Verified both new rules via `internal/rbac.MatchRoute` (temporary test against the live `rbac.yaml`) before and after, confirming no existing route's resolved permission changed
- Updated `internal/docs/openapi.yaml` to drop the now-resolved "RBAC GAP" notes on these three path items and set their real `x-required-permission`
- The third finding from 0.6.0 (`DELETE .../artifacts/{id}/types/{typeId}` unexpectedly requiring `index:artifacts:delete` via the same trailing-wildcard coupling) is left as-is per explicit decision — it already enforces a permission, just a stricter one than expected, so it's a separate design question rather than an open-access gap

## [0.6.0] — 2026-07-07

### Added
- `internal/docs/openapi.yaml` now explicitly documents 62 previously-generic proxy routes across fusion-forge, fusion-index, fusion-weave, and fusion-content — full request/response schemas transcribed from each upstream service's own DTOs/CRD types (fusion-index's own `openapi.yaml`, fusion-forge's `internal/api/dto/*`, fusion-flux's `api/v1alpha1/*_types.go` + `internal/apiserver`/`internal/monitoring` handlers, fusion-content's `internal/help`/`internal/videostore` DTOs)
- Each new path item carries `x-required-permission` (and `x-resource-type` where applicable), cross-checked against the live `rbac.yaml` via `internal/rbac.MatchRoute` rather than hand-derived, to avoid documenting a permission the router doesn't actually enforce
- New `forge`, `index`, `weave`, `weave-monitoring`, and `content` OpenAPI tags group the newly-documented routes; the existing generic `/api/<service>/{path}` `proxy` path items are kept as an explicit fallback for any route not yet itemized (e.g. a new upstream endpoint not yet reflected here)
- Kubernetes-native nested types referenced by fusion-weave's CRDs (`EnvVar`, `ResourceRequirements`, `Probe`, `SecurityContext`, `Job`, `Deployment`, `Event`, `ObjectMeta`) are modeled as loose/opaque objects rather than fully reproduced from the k8s.io OpenAPI definitions

### Fixed (documentation only — behavior unchanged)
- Flagged four RBAC coverage gaps discovered while cross-checking permissions against `MatchRoute`, documented inline in the new spec rather than silently patched in `rbac.yaml`:
  - `PUT /api/index/api/v1/artifacts/{id}` (update description) and `PUT /api/index/api/v1/artifacts/{id}/types/{typeId}` (assign type) match no route rule — open to any authenticated caller
  - `DELETE /api/index/api/v1/artifacts/{id}/types/{typeId}` (unassign type) unexpectedly requires `index:artifacts:delete` — the trailing wildcard on the artifact-delete rule absorbs the `/types/{typeId}` suffix
  - `PUT /api/weave/api/v1/runs/{name}` (full replace) matches no route rule — open to any authenticated caller, unlike every other weave resource which has an explicit PUT rule

## [0.5.1] — 2026-07-02

### Added
- `weave:batchtriggers:write` / `weave:batchtriggers:delete` permissions granted to `admin` (write+delete) and `engineer` (write only), for fusion-weave's new `BatchCron` trigger type REST endpoints (`/api/v1/batchtriggers`, `/validate`, `/{name}/stop`, `/{name}/resume`)
- `weave:kafkatriggers:write` / `weave:kafkatriggers:delete` permissions granted to `admin` (write+delete) and `engineer` (write only), for fusion-weave's new `Kafka` trigger type REST endpoints (`/api/v1/kafkatriggers`)
- Route permission rules for `DELETE/PUT/PATCH/POST /api/weave/api/v1/batchtriggers*` and `DELETE/PUT/PATCH/POST /api/weave/api/v1/kafkatriggers*`, placed before the `GET /api/weave/*` catch-all; GET list/get for both resources already covered by the existing catch-all under `weave:resources:read`
- OpenAPI spec version bumped to match; no path changes required since `/api/weave/{path}` already documents the proxy generically and defers to `rbac.yaml` for per-route permissions

## [0.5.0] — 2026-06-23

### Fixed
- `test/e2e/e2e_test.go` no longer fails to build — `api.NewRouter` call was missing the `content` proxy and the `adminH`/`resourcePermH`/`systemHealthH` params added in earlier releases; added a content upstream/proxy mirroring forge/index/weave and `nil` for the unused handler params
- `flux/prod-fusion/helmrelease.yaml` chart version constraint was pinned to `~0.1.0`, which never tracked any release past `0.1.x` (chart has been at `0.4.x`+ for several releases); changed to `>=0.4.0` so prod can pick up current and future chart versions

### Added
- OpenAPI spec (`internal/docs/openapi.yaml`) documenting `/bff/*` endpoints and the `/api/*` proxy routes; embedded into the binary via `go:embed`
- `GET /bff/openapi.yaml` — serves the raw spec
- `GET /bff/docs/*` — Swagger UI (via `swaggo/gin-swagger` + `swaggo/files`) for browsing the spec; both routes are public/unauthenticated and purely additive — no existing route, handler, or middleware was changed

## [0.4.8] — 2026-06-02

### Added
- `forge:admin:manage` permission granted to `admin` role for the new fusion-forge zombie-cleanup endpoint
- Route permission rule: `POST /api/forge/api/v1/builds/zombie-cleanup` → `forge:admin:manage` (admin-only; cleans up PENDING/BUILDING builds whose CIBuild CR no longer exists in K8s)

## [0.4.7] — 2026-05-27

### Added
- `forge:builds:delete` permission granted to `admin` and `engineer` roles (bulk build delete from fusion-forge `BuildsHandler`)
- Route permission rule: `DELETE /api/forge/api/v1/builds` → `forge:builds:delete`, placed before the `GET /api/forge/*` catch-all

## [0.4.6] — 2026-05-27

### Added
- `index:metrics:read` permission added to `admin`, `engineer`, and `viewer` roles in `rbac.yaml`
- Route permission rule: `GET /api/index/q/metrics` → `index:metrics:read`, placed before the `GET /api/index/*` catch-all (fusion-index aggregate metrics endpoint; TTL-cached on the index side via `METRICS_CACHE_TTL`)

## [0.4.5] — 2026-05-21

### Added
- `forge:gitwatchers:write` and `forge:gitwatchers:delete` permissions granted to `admin` and `engineer` roles (GitWatcher CRUD from fusion-forge GitOps watcher)
- Route permission rules: `POST /api/forge/api/v1/gitwatchers` + `PUT /api/forge/api/v1/gitwatchers/*` → `forge:gitwatchers:write`; `DELETE /api/forge/api/v1/gitwatchers/*` → `forge:gitwatchers:delete`; `GET` list/get remain covered by the existing `forge:builds:read` catch-all (viewer + engineer + admin)
- `weave:monitoring:read` permission granted to `admin`, `engineer`, and `viewer` roles
- Route permission rule: `GET /api/weave/monitor/v1/*` → `weave:monitoring:read`, placed before the general `GET /api/weave/*` catch-all to allow independent gating of the fusion-weave Monitoring API (`/monitor/v1/runs`, `/monitor/v1/stats/runs`, `/monitor/v1/chains/{name}/stats`)

## [0.4.3] — 2026-05-21

### Added
- `weave:runs:write` permission (create WeaveRun) granted to `admin` and `engineer` roles
- `weave:runs:delete` permission (delete WeaveRun) granted to `admin` role only
- Route permission rules: `POST /api/weave/api/v1/runs` → `weave:runs:write`; `DELETE /api/weave/api/v1/runs/*` → `weave:runs:delete`
- Rule ordering: DELETE runs first, then POST `/stop` action, then PATCH, then POST create — consistent with other weave resource blocks

## [0.4.2] — 2026-05-21

### Added
- `content:help:read` permission added to `admin`, `engineer`, and `viewer` roles in `rbac.yaml`
- Route permission rule: `GET /api/content/api/v1/help*` → `content:help:read` (placed before the existing `content:changelog:read` wildcard so help and changelog are independently gated)

## [0.4.1] — 2026-05-20

### Added
- RBAC route_permissions for `POST /api/forge/api/v1/appbuilds` and `POST /api/forge/api/v1/appbuilds/validate`, gated by `forge:builds:create`; existing `GET /api/forge/*` rule already covers list, get, and logs for app builds

## [0.4.0] — 2026-05-19

### Added
- Structured logging via `log/slog`: JSON output by default, configurable via `LOG_LEVEL` (`debug`|`info`|`warn`|`error`) and `LOG_FORMAT` (`json`|`text`) env vars
- `NewLoggingMiddleware()` in `internal/api/middleware/logging.go`: per-request logger with `request_id`, `method`, `path`, `client_ip`; emits one access log line per request
- `LoggerFromCtx(c)` helper for handlers and middleware to attach to the per-request logger
- `internalError(c, err)` shared handler helper that logs the error and writes a 500 response
- Helm values `config.logLevel` and `config.logFormat` wired through ConfigMap as `LOG_LEVEL`/`LOG_FORMAT`
- Debug-level logging for auth rejections (invalid Bearer token, allowlist block) in `APIAuth` and `Auth` middleware
- Debug-level logging for silent session token refresh failures in `APIAuth`

### Changed
- Replaced all `log.Printf`/`log.Fatalf`/`log.Println` calls with structured `slog.*` calls throughout `main.go`, `auth.go`, `apiauth.go`, `system_health.go`, and `mockoidc/server.go`
- Health probe goroutines now carry the per-request `*slog.Logger` (with `request_id`) for correlated probe-failure log lines
- `revokeRefreshToken` now accepts a `*slog.Logger` parameter instead of calling `log.Printf` directly
- Router replaced `gin.Logger()` with the new structured logging middleware (`gin.New()` was already in use)
- SA token read failure in `proxy/upstream.go` now logs via `slog.Error` before returning 502
- All previously-silent HTTP 500 paths in admin, resource-permissions, and system-health handlers now log the underlying error before responding

### Added
- Run stop proxy: `POST /api/weave/api/v1/runs/:name/stop` forwarded to `POST /api/v1/runs/{name}/stop` on fusion-flux; gated by `weave:steps:restart` permission (admin and engineer roles)
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
