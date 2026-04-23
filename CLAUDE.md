# fusion-bff

Backend for Frontend for the fusion platform GUI.

fusion-bff sits between the Vue.js web GUI and the internal fusion platform services (fusion-forge, fusion-index, fusion-weave). It validates OIDC tokens from human users, enforces an allowlist, and forwards requests to backend services using its own Kubernetes service account token with the authenticated user identity passed as a trusted header.

## Purpose

- **OIDC authentication** — validate JWT tokens issued by the configured OIDC provider (Keycloak, Dex, etc.)
- **User allowlist** — reject authenticated users who are not permitted to use the platform
- **Identity forwarding** — pass the resolved user identity (`X-User-ID`, `X-User-Email`) to upstream services as trusted headers
- **Proxy / routing** — forward API calls to fusion-forge, fusion-index, and fusion-weave; optionally aggregate responses
- **Pod-to-pod traffic is not routed through the BFF** — Kubernetes SA TokenReview auth on forge/index handles that path directly

## Stack

- **Go 1.25**, **Gin** (REST API + reverse proxy); module `github.com/fusion-platform/fusion-bff`
- **OIDC JWT validation** — `github.com/coreos/go-oidc/v3` against JWKS; custom `cachingKeySet` wrapper adds configurable TTL on top of `RemoteKeySet`
- **License**: GPL-3.0

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
       validate id_token, check allowlist → set HttpOnly sid cookie, redirect to /
  POST /bff/logout → revoke refresh token, delete session, clear cookie, redirect to end_session
  GET /bff/userinfo → returns {sub, email, name} from session

/api/* middleware (APIAuth):
  1. Cookie sid present → load session; silent refresh if access token within 30 s of expiry
  2. No valid cookie → fall back to Bearer <OIDC JWT> (service-to-service path, unchanged)
  3. Either path → set X-User-ID / X-User-Email on upstream request

Notes:
  - OIDC_ISSUER_URL (cluster-internal) used for token/revoke calls; OIDC_PUBLIC_AUTH_URL for browser redirects
  - Session state is in-memory (InMemoryStore) — single-pod only, no distributed support
  - CookieDomain "auto" derives .parent-domain from Host header for subdomain cookie sharing
```

### Pod-to-pod flow (existing, not in this service)
```
Pod → Bearer <K8s SA token> → fusion-forge / fusion-index
  TokenReview validates SA directly — no BFF involved
```

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

## Local dev (minikube)
- Minikube deployment uses tag `fusion-bff:local` — build with `docker build -t fusion-bff:local .` (not `latest`)
- Dev Vite server: `POST_LOGIN_REDIRECT_URL=http://dev.fusion.local:5174`, `CORS_ORIGINS=http://dev.fusion.local:5174`
- In-cluster spectra (bypass mode): `POST_LOGIN_REDIRECT_URL=http://spectra.fusion.local/`, `CORS_ORIGINS=http://spectra.fusion.local`, `SESSION_COOKIE_DOMAIN=auto` — see DEV.md for complete Helm commands
- Upstream proxy (`internal/proxy/upstream.go`) strips CORS headers in `ModifyResponse` — prevents duplicate `Access-Control-Allow-Origin` when upstream also sets it

## Minikube testing gotchas

- **OIDC issuer mismatch**: BFF rejects tokens fetched via `localhost` port-forward — the `iss` claim won't match `OIDC_ISSUER_URL`. Fetch tokens from inside the cluster: `kubectl run token-fetch --rm -i --restart=Never --image=alpine/curl:latest --namespace fusion -- sh -c 'curl -s -X POST http://keycloak.default.svc.cluster.local:8080/realms/fusion/protocol/openid-connect/token -d "grant_type=password&client_id=fusion-gui&client_secret=<secret>&username=testuser&password=password"' 2>/dev/null | grep -o '"access_token":"[^"]*"' | cut -d'"' -f4`
- **Keycloak `fusion-gui` is a confidential client** — always pass `client_secret` when fetching tokens; omitting it returns `unauthorized_client`
- **Helm field manager conflicts**: Resources originally created with `kubectl apply` or `kubectl patch` block `helm upgrade`. Fix: `kubectl patch configmap <name> -n <ns> --type merge -p '{"data":{...}}'` for ConfigMap keys; `kubectl set env deployment/<name>` for Deployment env vars; always follow with `kubectl rollout restart deployment/<name> -n <ns>`
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
  server/main.go           # Entry point — config + gin
internal/
  config/config.go         # Env var loading (bypass fields: OIDCBypass, OIDCBypassBaseURL, etc.)
  allowlist/allowlist.go   # Checker interface + static impl + WithTTLCache wrapper
  oidc/
    claims.go              # UserClaims struct
    validator.go           # JWT validation against JWKS endpoint (production)
    jwks.go                # JWKS fetching and caching (cachingKeySet with TTL)
  mockoidc/                # Embedded mock OIDC — active only when OIDC_BYPASS=true
    server.go              # RSA key gen, login form, auth code store, mock route handlers
    validator.go           # mockValidator — verifies JWTs using in-memory key (no HTTP)
  token/provider.go        # SA token Provider interface + FileProvider (TTL cache)
  proxy/
    upstream.go            # UpstreamProxy (single type used for forge, index, and weave)
  session/
    session.go             # Session struct, Store interface, InMemoryStore, CookieDomain helper, Reap
  api/
    handler/health.go      # /health /livez /readyz
    handler/auth.go        # /bff/login, /bff/callback, /bff/logout, /bff/userinfo
    middleware/auth.go     # OIDC Bearer middleware — validate + allowlist + set user context
    middleware/apiauth.go  # /api/* combined middleware: session cookie (with silent refresh) + Bearer fallback
    middleware/cors.go     # CORS middleware
    middleware/requestid.go
    router.go              # Gin routes
test/e2e/                  # e2e tests (build tag: e2e); uses httptest mock OIDC + upstreams
deployment/                # Helm chart
flux/                      # Flux GitOps (3 environments)
Dockerfile
Makefile
```
