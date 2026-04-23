# Installation

This document covers three deployment paths: local development, Docker, and Kubernetes (minikube or production).

---

## Prerequisites

| Tool | Minimum version | Purpose |
|---|---|---|
| Go | 1.22 | Build and test |
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

Minimum required values:

```dotenv
OIDC_ISSUER_URL=https://keycloak.example.com/realms/fusion
OIDC_CLIENT_ID=fusion-gui
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

## Docker

### Build

```bash
make docker-build IMG=fusion-bff:local
# or:
docker build -t fusion-bff:local .
```

The multi-stage Dockerfile uses `golang:1.25-alpine` to build a statically linked binary and copies it into `gcr.io/distroless/static-debian12:nonroot`. The final image has no shell and runs as a non-root user.

### Run

```bash
docker run \
  -e OIDC_ISSUER_URL=https://keycloak.example.com/realms/fusion \
  -e OIDC_CLIENT_ID=fusion-gui \
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

### Configure secrets

The chart expects an existing `fusion-bff-secret` Secret in the target namespace with the following keys:

```bash
kubectl create secret generic fusion-bff-secret \
  --namespace fusion \
  --from-literal=OIDC_ISSUER_URL=http://keycloak.default.svc.cluster.local:8080/realms/fusion \
  --from-literal=OIDC_CLIENT_ID=fusion-gui \
  --from-literal=ALLOWED_USERS=alice@example.com,bob@example.com
```

### Install with Helm

```bash
helm install fusion-bff ./deployment \
  --namespace fusion \
  --set image.repository=fusion-bff \
  --set image.tag=local \
  --set image.pullPolicy=Never \
  --set config.oidcIssuerUrl=http://keycloak.default.svc.cluster.local:8080/realms/fusion \
  --set config.oidcClientId=fusion-gui \
  --set config.forgeUrl=http://fusion-forge.fusion.svc.cluster.local:8080 \
  --set config.indexUrl=http://fusion-index-backend.fusion.svc.cluster.local:8080 \
  --set config.weaveUrl=http://fusion-weave-api.fusion.svc.cluster.local:8082
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
