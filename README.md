# fusion-bff

Backend for Frontend for the [fusion platform](../README.md) GUI.

`fusion-bff` sits between the Vue.js web GUI and the internal fusion platform services (`fusion-forge`, `fusion-index`, `fusion-weave`). It handles browser-based OIDC login (PKCE + session cookies), validates tokens, enforces a user allowlist, replaces the inbound credential with the BFF's own Kubernetes service account token, and forwards the resolved user identity as trusted headers.

---

## Why this exists

Direct calls from the GUI to `fusion-forge` / `fusion-index` / `fusion-weave` would require those services to validate OIDC JWTs from end-users, which complicates their auth model and exposes their internal endpoints. The BFF pattern centralises that concern:

- **One OIDC integration point** — only the BFF trusts the OIDC provider
- **Browser-safe auth** — PKCE login flow; tokens never touch the browser; session held server-side as an HttpOnly cookie
- **Uniform upstream auth** — all three upstream services always see a K8s SA token, never a user JWT
- **Identity forwarding** — `X-User-ID` / `X-User-Email` headers let upstream services act on behalf of the user without re-validating the original token
- **SA token isolation** — forge/index and weave use separate projected SA tokens with different audience scopes (required for weave's K8s TokenReview)

Pod-to-pod traffic (e.g. CI pipelines calling forge directly) bypasses the BFF entirely and uses K8s TokenReview.

---

## Features

| Capability | Detail |
|---|---|
| Browser PKCE login | `/bff/login` → Keycloak redirect; `/bff/callback` exchanges code, validates id_token, sets HttpOnly `sid` cookie |
| Session management | Server-side in-memory sessions; silent access token refresh when within 30 s of expiry |
| Logout | Revokes refresh token, deletes session, clears cookie, redirects to Keycloak `end_session` |
| User info | `GET /bff/userinfo` returns `{sub, email, name, roles, permissions, resource_permissions}` from the active session |
| RBAC | Config-driven roles + permissions (`rbac.yaml`); route-level enforcement via `APIAuth` middleware; resource-scoped grants in PostgreSQL |
| Admin API | `/bff/admin/group-roles` (CRUD), `/bff/admin/resource-permissions` (CRUD), `/bff/admin/rbac-config` (read) — require `admin:roles:manage` |
| OIDC JWT validation | RS256 signature check against JWKS; configurable cache TTL; Bearer fallback for service-to-service calls |
| User allowlist | Match `sub` or `email` claim; empty list = any authenticated user |
| Identity forwarding | `X-User-ID` (sub), `X-User-Email` injected on every upstream request |
| SA token rotation | Reads K8s projected tokens from disk with configurable TTL cache |
| Proxy routing | `/api/forge/*` → `fusion-forge`; `/api/index/*` → `fusion-index`; `/api/weave/*` → `fusion-weave` |
| SA token isolation | Separate projected token per upstream; weave token has no audience restriction for K8s TokenReview compatibility |
| CORS | Configurable allowed origins via `CORS_ORIGINS` |
| Health endpoints | `/health`, `/livez`, `/readyz` — no auth required |
| Graceful shutdown | 15 s drain on SIGTERM |
| Mock OIDC bypass | `OIDC_BYPASS=true` starts an embedded OIDC server on the same port; full PKCE flow works with a browser login form — no Keycloak needed for dev/testing |

---

## Quick start

```bash
# Prerequisites: Go 1.22+, a running OIDC provider, and the upstream services

cp .env.example .env          # fill in OIDC_ISSUER_URL, OIDC_CLIENT_ID, OIDC_CLIENT_SECRET, …
make run                      # starts on :8080

# Or with Docker
make docker-build IMG=fusion-bff:local
docker run --env-file .env -p 8080:8080 fusion-bff:local
```

### Quick start without Keycloak (mock OIDC bypass)

No OIDC provider needed. The BFF starts an embedded mock OIDC server on the same port and serves a browser login form at `/bff/login`.

```bash
OIDC_BYPASS=true \
OIDC_BYPASS_BASE_URL=http://localhost:8080 \
FORGE_URL=http://localhost:8081 \
INDEX_URL=http://localhost:8082 \
WEAVE_URL=http://localhost:8083 \
K8S_SA_TOKEN_PATH=/tmp/sa-token \
WEAVE_SA_TOKEN_PATH=/tmp/weave-sa-token \
make run
```

Open `http://localhost:8080/bff/login` in a browser. A pre-filled login form appears; submit it to create a session. All `OIDC_*` and `SESSION_SECRET` variables are optional in bypass mode.

> **Never set `OIDC_BYPASS=true` in a production environment.** It disables all real token validation and the user allowlist.

---

## Auth flows

### Browser login (PKCE)

```
GET  /bff/login     → redirect to Keycloak with code_challenge (S256)
GET  /bff/callback  → exchange code, validate id_token, create session → set sid cookie, redirect to /
POST /bff/logout    → revoke refresh token, delete session, clear cookie → redirect to end_session
GET  /bff/userinfo  → {sub, email, name} from session  (401 if no session)
```

### API requests (`/api/*`)

```
1. sid cookie present  → load session; refresh access token if within 30 s of expiry
2. No valid session    → fall back to Bearer <OIDC JWT> (service-to-service, unchanged)
Either path            → set X-User-ID / X-User-Email on upstream request
```

---

## Configuration

All configuration is via environment variables.

### Required

| Variable | Description |
|---|---|
| `OIDC_ISSUER_URL` | OIDC provider issuer URL (cluster-internal, used for token/revoke calls) |
| `OIDC_CLIENT_ID` | Expected `aud` claim value (e.g. `fusion-gui`) |

### OIDC / session

| Variable | Default | Description |
|---|---|---|
| `OIDC_CLIENT_SECRET` | — | Client secret for authorization_code exchange |
| `OIDC_REDIRECT_URL` | — | Callback URL registered in Keycloak (e.g. `https://bff.fusion.local/bff/callback`) |
| `OIDC_PUBLIC_AUTH_URL` | `OIDC_ISSUER_URL` | Browser-visible Keycloak base URL for auth redirects (set when public URL differs from cluster-internal issuer) |
| `OIDC_JWKS_URL` | `{issuer}/protocol/openid-connect/certs` | Override JWKS endpoint (required for non-Keycloak providers) |
| `OIDC_JWKS_CACHE_TTL` | `15m` | How often to force-refresh the JWKS key set |
| `OIDC_REVOKE_URL` | `{issuer}/protocol/openid-connect/revoke` | Token revocation endpoint |
| `OIDC_END_SESSION_URL` | `{publicAuthURL}/protocol/openid-connect/logout` | Keycloak end_session (browser redirect on logout) |
| `SESSION_COOKIE_NAME` | `sid` | Session cookie name |
| `SESSION_COOKIE_DOMAIN` | `""` | Cookie Domain: `""` = omit, `"auto"` = derive `.parent` from Host header, or a literal value |
| `SESSION_COOKIE_SECURE` | `false` | Set `true` in production (HTTPS) |
| `SESSION_MAX_AGE` | `8h` | Maximum session lifetime |

### RBAC

| Variable | Default | Description |
|---|---|---|
| `RBAC_CONFIG_PATH` | `./rbac.yaml` | Path to the RBAC config file (ConfigMap-mounted in K8s) |
| `DB_DSN` | — | PostgreSQL DSN — required when `rbac.yaml` `group_source` is `db` or `both` |

### Development bypass (mock OIDC) — never use in production

| Variable | Default | Description |
|---|---|---|
| `OIDC_BYPASS` | `false` | `true` = start embedded mock OIDC; skips all real token validation and allowlist |
| `OIDC_BYPASS_BASE_URL` | `http://localhost:{HTTP_PORT}` | Browser-visible BFF URL used to build mock OIDC redirect URLs |
| `OIDC_BYPASS_SUB` | `dev-user` | Default `sub` claim pre-filled in the mock login form |
| `OIDC_BYPASS_EMAIL` | `dev@local` | Default `email` claim pre-filled in the mock login form |
| `OIDC_BYPASS_NAME` | `Dev User` | Default display name pre-filled in the mock login form |
| `OIDC_BYPASS_GROUPS` | — | Comma-separated groups pre-selected in the mock login form |

When `OIDC_BYPASS=true`, `OIDC_ISSUER_URL`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, and `OIDC_REDIRECT_URL` are all auto-derived and do not need to be set.

### Allowlist / upstreams

| Variable | Default | Description |
|---|---|---|
| `ALLOWED_USERS` | — | Comma-separated `sub` or `email` values; empty = allow any authenticated user |
| `CORS_ORIGINS` | — | Comma-separated allowed CORS origins |
| `FORGE_URL` | `http://fusion-forge.fusion.svc.cluster.local:8080` | fusion-forge base URL |
| `INDEX_URL` | `http://fusion-index-backend.fusion.svc.cluster.local:8080` | fusion-index base URL |
| `WEAVE_URL` | `http://fusion-weave-api.fusion.svc.cluster.local:8082` | fusion-weave API server base URL |
| `K8S_SA_TOKEN_PATH` | `/var/run/secrets/kubernetes.io/serviceaccount/token` | SA token for forge/index (audience: fusion-bff) |
| `WEAVE_SA_TOKEN_PATH` | `/var/run/secrets/fusion-bff/weave/token` | SA token for weave (no audience; required for K8s TokenReview) |
| `SA_TOKEN_CACHE_TTL` | `5m` | Re-read SA tokens from disk after this interval |
| `ALLOWLIST_CACHE_TTL` | `30s` | Per-result cache TTL (only relevant for custom DB-backed checkers) |
| `HTTP_PORT` | `8080` | Listen port |

---

## Makefile targets

```
make build        Build the binary to bin/fusion-bff
make test         Run unit tests with race detector
make test-e2e     Run e2e tests (no external services needed)
make docker-build Build Docker image (IMG=fusion-bff:latest)
make run          Run locally, sourcing .env if present
make lint         Run golangci-lint
make fmt          Format source with gofmt
make tidy         Tidy go.mod / go.sum
make help         Show all targets
```

---

## License

GPL-3.0 — see [LICENSE](LICENSE).
