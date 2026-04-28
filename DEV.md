# Local Dev Setup — Mock OIDC + fusion-spectra

This document covers the exact configuration required to run fusion-bff with the embedded mock OIDC server alongside [fusion-spectra](../fusion-spectra) in a local minikube cluster. No Keycloak instance is needed.

---

## Prerequisites

| Tool | Purpose |
|---|---|
| minikube | Local Kubernetes cluster |
| kubectl | Cluster access |
| Helm 3 | Chart install/upgrade |
| Docker | Image builds inside minikube |

Enable the ingress addon (one-time):

```bash
minikube addons enable ingress
```

---

## /etc/hosts

Both services are reached via hostnames that the browser and the BFF itself must resolve to minikube:

```bash
echo "$(minikube ip)  bff.fusion.local spectra.fusion.local" | sudo tee -a /etc/hosts
```

- `bff.fusion.local` — BFF ingress; used by the browser for the login flow and by spectra for API calls
- `spectra.fusion.local` — Vue GUI ingress; where the browser lands after login

---

## URL relationships

The following URLs must be consistent across both charts:

| What | Value | Used in |
|---|---|---|
| BFF public URL | `http://bff.fusion.local` | `oidcBypassBaseUrl`, spectra `bffUrl` |
| OIDC callback | `http://bff.fusion.local/bff/callback` | auto-derived from `oidcBypassBaseUrl` — no extra config |
| Post-login redirect | `http://spectra.fusion.local/` | `postLoginRedirectUrl` |
| CORS allowed origin | `http://spectra.fusion.local` | `corsOrigins` |
| Session cookie domain | `auto` | derives `.fusion.local` → shared between BFF and spectra subdomains |

### Why `oidcBypassBaseUrl` drives everything

In bypass mode `config.go` auto-derives several URLs from `oidcBypassBaseUrl`:

```
OIDC_ISSUER_URL     = http://localhost:8080/mock-oidc          (cluster-internal — token calls)
OIDC_PUBLIC_AUTH_URL = http://bff.fusion.local/mock-oidc        (browser — auth redirect)
OIDC_REDIRECT_URL   = http://bff.fusion.local/bff/callback      (auto-derived)
OIDC_REVOKE_URL     = http://localhost:8080/mock-oidc/.../revoke (cluster-internal — logout)
OIDC_END_SESSION_URL = http://bff.fusion.local/mock-oidc/.../logout (browser — end session)
```

You do **not** need to set `oidcRedirectUrl`, `oidcIssuerUrl`, `oidcClientId`, `oidcClientSecret`, or `oidcPublicAuthUrl` when bypass is active.

---

## Build images inside minikube

Both images must be built using minikube's Docker daemon so they are available without a registry:

```bash
# Point your local Docker client at minikube's daemon
eval $(minikube docker-env)

# Build fusion-bff
cd /path/to/fusion-bff
docker build -t fusion-bff:0.1.0 .

# Build fusion-spectra
cd /path/to/fusion-spectra
docker build -t fusion-spectra:0.1.0 .
```

After a code change, rebuild the image and restart the deployment:

```bash
eval $(minikube docker-env)
docker build -t fusion-bff:0.1.0 .            # bump semver on each deploy
kubectl rollout restart deployment/fusion-bff -n fusion
```

---

## Deploy fusion-bff (bypass mode)

No Secret is required — bypass mode ignores `oidcClientSecret` and `sessionSecret`.

```bash
helm upgrade --install fusion-bff ./deployment \
  --namespace fusion --create-namespace \
  --set image.repository=fusion-bff \
  --set image.tag=local \
  --set image.pullPolicy=Never \
  --set secret.create=false \
  --set config.oidcBypass=true \
  --set config.oidcBypassBaseUrl=http://bff.fusion.local \
  --set config.oidcBypassEmail=dev@local \
  --set config.oidcBypassName="Dev User" \
  --set config.oidcBypassSub=dev-user \
  --set config.sessionCookieDomain=auto \
  --set config.sessionCookieSecure=false \
  --set config.postLoginRedirectUrl=http://spectra.fusion.local/ \
  --set config.corsOrigins=http://spectra.fusion.local \
  --set config.forgeUrl=http://fusion-forge.fusion.svc.cluster.local:8080 \
  --set config.indexUrl=http://fusion-index-backend.fusion.svc.cluster.local:8080 \
  --set config.weaveUrl=http://fusion-weave-api.fusion.svc.cluster.local:8082
```

To customise the default identity shown in the mock login form, change `oidcBypassEmail`, `oidcBypassName`, and `oidcBypassSub`. The form fields are editable before submission, so you can test multiple identities without redeploying.

---

## Deploy fusion-spectra

Use the `values-dev.yaml` override file in the spectra repo:

```bash
cd /path/to/fusion-spectra

helm upgrade --install fusion-spectra ./deployment \
  -f ./deployment/values-dev.yaml \
  --namespace fusion \
  --set config.bffUrl=http://bff.fusion.local
```

The only BFF-relevant value in spectra is `config.bffUrl`. It must match the BFF's public ingress hostname.

---

## Database in development (group_source: db or both)

When testing the DB-backed RBAC store in minikube, pass the DSN via `db.create=true` so the chart generates the Secret — no ESO required in dev:

```bash
helm upgrade --install fusion-bff ./deployment \
  --namespace fusion \
  --set db.create=true \
  --set db.dsn="postgres://fusion:devpass@fusion-index-postgresql.fusion.svc.cluster.local:5432/fusion_bff?sslmode=disable" \
  ... # (rest of your usual flags)
```

The DSN above reuses the fusion-index postgres pod; the `fusion_bff` database must be created once:

```bash
kubectl exec -n fusion fusion-index-postgresql-0 -- \
  bash -c "PGPASSWORD='<pg-admin-pass>' psql -U postgres -c 'CREATE DATABASE fusion_bff OWNER fusion;'"
```

The BFF runs `db.Migrate()` on startup — no manual schema setup needed. Seed initial group→role assignments via `POST /bff/admin/group-roles` (requires `admin:roles:manage`) or directly:

```bash
kubectl exec -n fusion fusion-index-postgresql-0 -- \
  bash -c "PGPASSWORD='<pg-admin-pass>' psql -U fusion fusion_bff -c \
  \"INSERT INTO group_role_assignments (group_name, role_name) VALUES ('platform-admin','admin');\""
```

---

## Helm field manager conflicts (workaround)

If either chart was previously patched with `kubectl patch` or `kubectl apply`, Helm's server-side apply may fail with:

```
conflict with "kubectl-patch" using v1: .data.CORS_ORIGINS
```

Workaround: patch the ConfigMap directly and restart instead of using `helm upgrade`:

```bash
# BFF ConfigMap quick-patch (without touching Helm ownership)
kubectl patch configmap fusion-bff -n fusion --type merge -p '{
  "data": {
    "OIDC_BYPASS": "true",
    "OIDC_BYPASS_BASE_URL": "http://bff.fusion.local",
    "OIDC_BYPASS_SUB": "dev-user",
    "OIDC_BYPASS_EMAIL": "dev@local",
    "OIDC_BYPASS_NAME": "Dev User",
    "SESSION_COOKIE_DOMAIN": "auto",
    "SESSION_COOKIE_SECURE": "false",
    "POST_LOGIN_REDIRECT_URL": "http://spectra.fusion.local/",
    "CORS_ORIGINS": "http://spectra.fusion.local"
  }
}'

kubectl rollout restart deployment/fusion-bff -n fusion
```

A ConfigMap change never restarts pods automatically — always follow it with `kubectl rollout restart`.

---

## Ingress

Both services need an Ingress object. Verify they exist and are assigned an address:

```bash
kubectl get ingress -n fusion
```

Expected output:

```
NAME             CLASS   HOSTS                   ADDRESS        PORTS
fusion-bff       nginx   bff.fusion.local        192.168.49.2   80
fusion-spectra   nginx   spectra.fusion.local    192.168.49.2   80
```

If an ingress is missing, check the chart's `ingress.enabled` value and the ingress class name (`nginx` for the minikube addon).

---

## Verification

```bash
# 1. BFF health (no auth required)
curl -s http://bff.fusion.local/health
# {"status":"ok"}

# 2. Unauthenticated API call should return 401
curl -s -o /dev/null -w "%{http_code}" http://bff.fusion.local/api/forge/api/v1/venvs
# 401

# 3. Full browser flow
open http://spectra.fusion.local
# → redirected to http://bff.fusion.local/bff/login
# → mock login form appears (yellow warning banner)
# → submit form
# → redirected to http://spectra.fusion.local/  (logged in)

# 4. Verify session cookie and userinfo
curl -s -c /tmp/bff-cookies.txt -b /tmp/bff-cookies.txt \
  http://bff.fusion.local/bff/userinfo | jq .
# {"sub":"dev-user","email":"dev@local","name":"Dev User"}

# 5. Authenticated API call through the session cookie
curl -s -b /tmp/bff-cookies.txt \
  http://bff.fusion.local/api/forge/api/v1/venvs | jq .
```

---

## Switching back to real Keycloak

```bash
helm upgrade fusion-bff ./deployment \
  --namespace fusion \
  --reuse-values \
  --set config.oidcBypass=false \
  --set config.oidcIssuerUrl=http://keycloak.default.svc.cluster.local:8080/realms/fusion \
  --set config.oidcClientId=fusion-gui \
  --set config.oidcRedirectUrl=http://bff.fusion.local/bff/callback \
  --set secret.create=true \
  --set secret.oidcClientSecret=<your-client-secret> \
  --set secret.sessionSecret=$(openssl rand -hex 32) \
  --set config.postLoginRedirectUrl=http://spectra.fusion.local/
```

See [INSTALL.md](INSTALL.md) for full Keycloak setup steps.

---

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| Browser stays on `/bff/login` after submit | `POST_LOGIN_REDIRECT_URL` wrong | Set to `http://spectra.fusion.local/` |
| spectra shows blank page after login | `config.bffUrl` wrong in spectra | Set to `http://bff.fusion.local` |
| API calls from spectra return 403/CORS error | `CORS_ORIGINS` missing spectra origin | Add `http://spectra.fusion.local` |
| Session cookie not shared between subdomains | `SESSION_COOKIE_DOMAIN` not `auto` | Set `sessionCookieDomain: auto` |
| BFF returns 401 despite logged-in session | Cookie not sent — different domain | Check `/etc/hosts` and cookie domain |
| `helm upgrade` fails with field manager conflict | Prior `kubectl patch` owns fields | Use `kubectl patch configmap` + `rollout restart` |
| `cannot pull image fusion-bff:local` | Built outside minikube daemon | Run `eval $(minikube docker-env)` first |
| Login form says token expired immediately | Clock skew between host and minikube | `minikube ssh -- sudo date -s "$(date -u +%Y-%m-%dT%H:%M:%SZ)"` |
