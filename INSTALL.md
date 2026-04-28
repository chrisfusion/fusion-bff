# Installation

This document covers three deployment paths: local development, Docker, and Kubernetes (minikube or production).

---

## Prerequisites

| Tool | Minimum version | Purpose |
|---|---|---|
| Go | 1.25 | Build and test |
| Docker | 20+ | Image build |
| kubectl | 1.26+ | Kubernetes deployment |
| Helm | 3.12+ | Chart rendering / install |
| golangci-lint | 1.57+ | Linting (optional) |

---

## Local development

### 1. Clone and install dependencies

```bash
git clone https://github.com/fusion-platform/fusion-bff.git
cd fusion-bff
go mod download
```

### 2. Configure environment

Copy the example env file and fill in your values:

```bash
cp .env.example .env
```

Minimum required values (with a real OIDC provider):

```dotenv
OIDC_ISSUER_URL=https://keycloak.example.com/realms/fusion
OIDC_CLIENT_ID=fusion-gui
OIDC_CLIENT_SECRET=your-client-secret
OIDC_REDIRECT_URL=http://localhost:8080/bff/callback
ALLOWED_USERS=alice@example.com,bob@example.com
FORGE_URL=http://localhost:8081
INDEX_URL=http://localhost:8082
WEAVE_URL=http://localhost:8083
K8S_SA_TOKEN_PATH=/tmp/sa-token         # dummy file for forge/index calls
WEAVE_SA_TOKEN_PATH=/tmp/weave-sa-token # dummy file for weave calls
```

Create dummy SA token files for local runs:

```bash
echo "dummy-token" > /tmp/sa-token
echo "dummy-weave-token" > /tmp/weave-sa-token
```

### 3. Run

```bash
make run
# or directly:
# set -a; . ./.env; set +a; go run ./cmd/server
```

The server listens on `:8080` by default. Override with `HTTP_PORT`.

### 4. Test

```bash
make test          # unit tests + race detector
make test-e2e      # e2e tests — no external services needed
```

---

## Local development without Keycloak (OIDC bypass mode)

When you don't have a Keycloak instance available, set `OIDC_BYPASS=true`. The BFF starts an embedded mock OIDC server on the same port and handles the full PKCE browser login flow internally. No `OIDC_*` variables are required.

> **Never use bypass mode in a production or staging environment.** It disables all real token validation and the allowlist.

### Minimum env for bypass mode

```dotenv
OIDC_BYPASS=true
OIDC_BYPASS_BASE_URL=http://localhost:8080   # must be the URL the browser uses to reach the BFF
# Optional identity pre-fill (form values are editable before submitting):
OIDC_BYPASS_SUB=alice
OIDC_BYPASS_EMAIL=alice@example.com
OIDC_BYPASS_NAME=Alice Example
# Upstream URLs and dummy SA token files still required:
FORGE_URL=http://localhost:8081
INDEX_URL=http://localhost:8082
WEAVE_URL=http://localhost:8083
K8S_SA_TOKEN_PATH=/tmp/sa-token
WEAVE_SA_TOKEN_PATH=/tmp/weave-sa-token
```

```bash
echo "dummy-token" > /tmp/sa-token
echo "dummy-weave-token" > /tmp/weave-sa-token
make run
```

Open `http://localhost:8080/bff/login` in a browser. A yellow-warned mock login form appears pre-filled with the configured identity. You can change `sub`, `email`, and `name` before submitting to test different user identities. Submitting creates a real server-side session and sets the `sid` cookie — the rest of the BFF behaves identically to production.

### How it works

When bypass is active the BFF registers these additional routes on the same Gin engine, mirroring Keycloak's path convention:

| Route | Purpose |
|---|---|
| `GET /mock-oidc/protocol/openid-connect/auth` | Renders the mock login form |
| `POST /mock-oidc/protocol/openid-connect/auth` | Issues an auth code, redirects to `/bff/callback` |
| `POST /mock-oidc/protocol/openid-connect/token` | Exchanges auth code for signed JWTs (RS256, 24 h expiry) |
| `GET /mock-oidc/protocol/openid-connect/certs` | Serves the JWKS (in-memory RSA public key) |
| `POST /mock-oidc/protocol/openid-connect/revoke` | No-op (returns 200) |
| `GET /mock-oidc/protocol/openid-connect/logout` | Redirects to `post_logout_redirect_uri` or `/` |

A fresh RSA-2048 keypair is generated at startup; tokens issued in one run are invalid after restart.

### Logout in bypass mode

Logout works normally — the browser is redirected to the mock end-session endpoint, which immediately redirects to `/`. No real token revocation occurs.

---

## Docker

### Build

```bash
make docker-build IMG=fusion-bff:local
# or:
docker build -t fusion-bff:local .
```

The multi-stage Dockerfile uses `golang:1.25-alpine` to build a statically linked binary and copies it into `gcr.io/distroless/static-debian12:nonroot`. The final image has no shell and runs as a non-root user.

### Run (with Keycloak)

```bash
docker run \
  -e OIDC_ISSUER_URL=https://keycloak.example.com/realms/fusion \
  -e OIDC_CLIENT_ID=fusion-gui \
  -e OIDC_CLIENT_SECRET=your-client-secret \
  -e OIDC_REDIRECT_URL=http://localhost:8080/bff/callback \
  -e ALLOWED_USERS=alice@example.com \
  -e FORGE_URL=http://host.docker.internal:8081 \
  -e INDEX_URL=http://host.docker.internal:8082 \
  -e WEAVE_URL=http://host.docker.internal:8083 \
  -e K8S_SA_TOKEN_PATH=/run/secrets/sa-token \
  -e WEAVE_SA_TOKEN_PATH=/run/secrets/weave-sa-token \
  -v /tmp/sa-token:/run/secrets/sa-token:ro \
  -v /tmp/weave-sa-token:/run/secrets/weave-sa-token:ro \
  -p 8080:8080 \
  fusion-bff:local
```

### Run (bypass mode — no Keycloak)

```bash
echo "dummy" > /tmp/sa-token && echo "dummy" > /tmp/weave-sa-token

docker run \
  -e OIDC_BYPASS=true \
  -e OIDC_BYPASS_BASE_URL=http://localhost:8080 \
  -e OIDC_BYPASS_EMAIL=alice@example.com \
  -e FORGE_URL=http://host.docker.internal:8081 \
  -e INDEX_URL=http://host.docker.internal:8082 \
  -e WEAVE_URL=http://host.docker.internal:8083 \
  -e K8S_SA_TOKEN_PATH=/run/secrets/sa-token \
  -e WEAVE_SA_TOKEN_PATH=/run/secrets/weave-sa-token \
  -v /tmp/sa-token:/run/secrets/sa-token:ro \
  -v /tmp/weave-sa-token:/run/secrets/weave-sa-token:ro \
  -p 8080:8080 \
  fusion-bff:local
```

Then open `http://localhost:8080/bff/login`.

---

## Kubernetes / minikube

### Prerequisites

- A running cluster with a `fusion` namespace
- Keycloak reachable from inside the cluster
- `fusion-forge`, `fusion-index-backend`, and `fusion-weave-api` running in the same namespace
- `flux` CLI installed if using GitOps (optional for manual Helm installs)

### Build image inside minikube

```bash
eval $(minikube docker-env)
make docker-build IMG=fusion-bff:local
```

### Install with Helm (normal mode)

```bash
helm install fusion-bff ./deployment \
  --namespace fusion \
  --set image.repository=fusion-bff \
  --set image.tag=local \
  --set image.pullPolicy=Never \
  --set config.oidcIssuerUrl=http://keycloak.default.svc.cluster.local:8080/realms/fusion \
  --set config.oidcClientId=fusion-gui \
  --set config.oidcRedirectUrl=http://bff.fusion.local/bff/callback \
  --set secret.oidcClientSecret=your-client-secret \
  --set secret.sessionSecret=$(openssl rand -hex 32) \
  --set config.forgeUrl=http://fusion-forge.fusion.svc.cluster.local:8080 \
  --set config.indexUrl=http://fusion-index-backend.fusion.svc.cluster.local:8080 \
  --set config.weaveUrl=http://fusion-weave-api.fusion.svc.cluster.local:8082
```

### Install with Helm (bypass mode — no Keycloak)

No Secret is needed. `OIDC_ISSUER_URL`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, and `OIDC_REDIRECT_URL` are all auto-derived from the bypass base URL.

```bash
helm install fusion-bff ./deployment \
  --namespace fusion \
  --set image.repository=fusion-bff \
  --set image.tag=local \
  --set image.pullPolicy=Never \
  --set config.oidcBypass=true \
  --set config.oidcBypassBaseUrl=http://bff.dev-fusion.local \
  --set config.oidcBypassEmail=alice@example.com \
  --set secret.create=false \
  --set config.forgeUrl=http://fusion-forge.fusion.svc.cluster.local:8080 \
  --set config.indexUrl=http://fusion-index-backend.fusion.svc.cluster.local:8080 \
  --set config.weaveUrl=http://fusion-weave-api.fusion.svc.cluster.local:8082
```

### Database (PostgreSQL)

Required only when `rbac.yaml` sets `group_source: db` or `both`. Three modes — pick one:

**Mode 1 — disabled** (default; `group_source: jwt`)

No extra flags needed. `DB_DSN` is not injected.

**Mode 2 — chart-generated Secret**

The chart creates a `<release>-db` Secret from the DSN you supply. Pass the value via `--set` or a values override file that is never committed to git.

```bash
helm upgrade --install fusion-bff ./deployment \
  --namespace fusion \
  --set db.create=true \
  --set db.dsn="postgres://fusion:devpass@postgres.fusion.svc.cluster.local:5432/fusion_bff?sslmode=disable" \
  ...
```

**Mode 3 — existing Secret (ESO / kubectl)**

Create the Secret out-of-band (or let ESO materialise it), then point the chart at it:

```bash
# One-time creation (or managed by ESO ExternalSecret)
kubectl create secret generic fusion-bff-db \
  --namespace fusion \
  --from-literal=DB_DSN="postgres://fusion:pass@postgres:5432/fusion_bff?sslmode=disable"

helm upgrade --install fusion-bff ./deployment \
  --namespace fusion \
  --set db.existingSecret=fusion-bff-db \
  ...
```

The key inside the Secret defaults to `DB_DSN`; override with `db.existingSecretKey` if your Secret uses a different key name.

---

To switch an existing bypass deployment back to real OIDC:

```bash
helm upgrade fusion-bff ./deployment \
  --namespace fusion \
  --reuse-values \
  --set config.oidcBypass=false \
  --set config.oidcIssuerUrl=http://keycloak.default.svc.cluster.local:8080/realms/fusion \
  --set config.oidcClientId=fusion-gui \
  --set config.oidcRedirectUrl=http://bff.fusion.local/bff/callback \
  --set secret.create=true \
  --set secret.oidcClientSecret=your-client-secret \
  --set secret.sessionSecret=$(openssl rand -hex 32)
```

Check rollout:

```bash
kubectl rollout status deployment/fusion-bff -n fusion
kubectl logs -l app.kubernetes.io/name=fusion-bff -n fusion
```

### Upgrade

```bash
helm upgrade fusion-bff ./deployment \
  --namespace fusion \
  --set image.tag=<new-tag>
```

### Uninstall

```bash
helm uninstall fusion-bff --namespace fusion
```

### Port-forward for local testing

```bash
kubectl port-forward -n fusion service/fusion-bff 18081:8080 --address 127.0.0.1
```

The BFF is then reachable at `http://localhost:18081`.

---

## Flux GitOps (production)

The `flux/` directory contains HelmRelease and Kustomization manifests for three environments:

| Environment | Namespace | Path |
|---|---|---|
| Development | `dev-fusion` | `flux/dev-fusion/` |
| Staging | `dev-staging-fusion` | `flux/dev-staging-fusion/` |
| Production | `prod-fusion` | `flux/prod-fusion/` |

Apply a specific environment:

```bash
kubectl apply -k flux/dev-fusion/
```

Secrets referenced by the HelmRelease must be created separately (SOPS-encrypted in a production setup). See the Flux documentation for secret management with `sops` and `age`.

---

## fusion-weave SA auth setup

When fusion-weave runs with `saAuthEnabled: true`, it validates the BFF's SA token via Kubernetes TokenReview and reads the caller's role from the `fusion-platform.io/role` label on the ServiceAccount.

The BFF Helm chart automatically sets this label on its ServiceAccount:

```yaml
# rendered by deployment/templates/serviceaccount.yaml
labels:
  fusion-platform.io/role: admin
```

The Helm upgrade applies this label on install/upgrade. If you need to apply it manually:

```bash
kubectl label serviceaccount fusion-bff -n fusion fusion-platform.io/role=admin --overwrite
```

The BFF uses a separate projected SA token for weave calls (`weave-sa-token` volume, no audience restriction). This is required because the existing forge/index token is scoped to `audience: fusion-bff`, which would fail Kubernetes TokenReview — the kube-apiserver validates tokens against its own audience by default.

To enable SA auth on fusion-weave:

```bash
kubectl set env deployment/fusion-weave-api -n fusion \
  AUTH_SA=true \
  ALLOW_UNAUTHENTICATED=false
```

Or via Helm upgrade of the fusion-weave chart:

```bash
helm upgrade fusion-weave ./path/to/fusion-weave-chart \
  --namespace default \
  --reuse-values \
  --set api.auth.saAuthEnabled=true \
  --set api.auth.allowUnauthenticated=false
```

---

## Keycloak setup (minikube example)

These steps configure a minimal Keycloak realm so the BFF can validate tokens issued to the GUI client.

> **Note on token acquisition:** In minikube the Keycloak service is ClusterIP — no NodePort. Port-forward it (`kubectl port-forward -n default service/keycloak 18080:8080`) and use `http://localhost:18080` for admin API calls. For user token requests that will be sent to the BFF, fetch the token from inside the cluster (e.g. via `kubectl run`) so the `iss` claim matches the BFF's `OIDC_ISSUER_URL` (the cluster-internal URL, not `localhost`). If the issuer doesn't match, the BFF returns 401 even with a valid signature.

```bash
# Get an admin token
ADMIN_TOKEN=$(curl -s -X POST \
  http://localhost:$(kubectl get svc keycloak -n default \
    -o jsonpath='{.spec.ports[0].nodePort}')/realms/master/protocol/openid-connect/token \
  -d "grant_type=password&client_id=admin-cli&username=admin&password=admin" \
  | jq -r .access_token)

KEYCLOAK=http://localhost:<nodePort>

# Create realm
curl -s -X POST $KEYCLOAK/admin/realms \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"realm":"fusion","enabled":true}'

# Create fusion-gui client with audience mapper
curl -s -X POST $KEYCLOAK/admin/realms/fusion/clients \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "clientId":"fusion-gui","enabled":true,"publicClient":true,
    "redirectUris":["*"],"webOrigins":["*"],
    "protocolMappers":[{
      "name":"fusion-gui-audience","protocol":"openid-connect",
      "protocolMapper":"oidc-audience-mapper",
      "config":{"included.client.audience":"fusion-gui","access.token.claim":"true"}
    }]
  }'

# Create a test user
curl -s -X POST $KEYCLOAK/admin/realms/fusion/users \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "username":"testuser","email":"testuser@example.com",
    "firstName":"Test","lastName":"User",
    "enabled":true,
    "credentials":[{"type":"password","value":"password","temporary":false}]
  }'
```
