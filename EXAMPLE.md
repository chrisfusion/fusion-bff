# Examples

All examples assume the BFF is running locally on port `18081` (via port-forward or `make run`).

```bash
BFF=http://localhost:18081
```

---

## Mock OIDC bypass (no Keycloak needed)

When the BFF is running with `OIDC_BYPASS=true`, you don't need a real OIDC token. The browser login flow works against the embedded mock server.

### Browser login via the mock login form

```bash
# Start the BFF in bypass mode
OIDC_BYPASS=true OIDC_BYPASS_BASE_URL=http://localhost:8080 \
  FORGE_URL=http://localhost:8081 \
  K8S_SA_TOKEN_PATH=/tmp/sa-token WEAVE_SA_TOKEN_PATH=/tmp/weave-sa-token \
  make run

# Open in browser — a mock login form appears
open http://localhost:8080/bff/login
```

After submitting the form, the BFF sets an `sid` session cookie. Subsequent `curl` calls can use that cookie:

```bash
# Extract the cookie from a browser session or use curl's cookie jar
curl -c /tmp/bff-cookies.txt -b /tmp/bff-cookies.txt \
  -s http://localhost:8080/bff/userinfo | jq .
# {"sub":"dev-user","email":"dev@local","name":"Dev User"}
```

### API calls with a mock Bearer token

In bypass mode the `mockValidator` accepts any RS256 token signed with the in-memory key. The simplest way to test API calls without a browser is to obtain a token directly from the mock token endpoint:

```bash
BFF=http://localhost:8080

# Step 1 — start a login flow to get a state value
STATE=$(curl -s -o /dev/null -w "%{redirect_url}" $BFF/bff/login \
  | grep -o 'state=[^&]*' | cut -d= -f2)

# Step 2 — submit the mock login form directly
curl -s -c /tmp/bff-cookies.txt \
  -d "state=$STATE&redirect_uri=$BFF/bff/callback&sub=alice&email=alice@example.com&name=Alice" \
  -L $BFF/mock-oidc/protocol/openid-connect/auth

# Step 3 — use the session cookie for API calls
curl -s -b /tmp/bff-cookies.txt \
  $BFF/api/forge/api/v1/venvs | jq .
```

### Obtain an access token from the mock token endpoint (service-to-service style)

The mock token endpoint also accepts `grant_type=refresh_token` with any previously issued refresh token, or you can use the embedded auth code flow via curl to get a Bearer token for use in scripts:

```bash
# After a browser login, the session cookie is more convenient.
# For pure Bearer token testing, check the session's access token via userinfo:
curl -s -b /tmp/bff-cookies.txt $BFF/bff/userinfo | jq .
```

---

## Obtain an OIDC token from Keycloak

### Public client

```bash
KEYCLOAK_URL=http://localhost:18080   # port-forwarded: kubectl port-forward -n default svc/keycloak 18080:8080
REALM=fusion
CLIENT_ID=fusion-gui

TOKEN=$(curl -s -X POST \
  "$KEYCLOAK_URL/realms/$REALM/protocol/openid-connect/token" \
  -d "grant_type=password&client_id=$CLIENT_ID&username=testuser&password=password" \
  | jq -r .access_token)
```

### Confidential client (requires client secret)

```bash
TOKEN=$(curl -s -X POST \
  "$KEYCLOAK_URL/realms/$REALM/protocol/openid-connect/token" \
  -d "grant_type=password&client_id=$CLIENT_ID&client_secret=fusion-gui-secret&username=testuser&password=password" \
  | jq -r .access_token)
```

> **Minikube issuer gotcha:** The BFF's `OIDC_ISSUER_URL` is the cluster-internal Keycloak URL (e.g. `http://keycloak.default.svc.cluster.local:8080/realms/fusion`). A token fetched via `localhost:18080` has `iss: http://localhost:18080/...`, which the BFF rejects with 401 even though the signature is valid. Fetch the token from inside the cluster to get the correct issuer:
>
> ```bash
> TOKEN=$(kubectl run token-fetch --rm -i --restart=Never \
>   --image=alpine/curl:latest --namespace fusion \
>   -- sh -c 'curl -s -X POST http://keycloak.default.svc.cluster.local:8080/realms/fusion/protocol/openid-connect/token \
>     -d "grant_type=password&client_id=fusion-gui&client_secret=fusion-gui-secret&username=testuser&password=password"' \
>   2>/dev/null | grep -o '"access_token":"[^"]*"' | cut -d'"' -f4)
> ```

---

## Health check (no auth required)

```bash
curl -s $BFF/health
# {"status":"ok"}

curl -s $BFF/livez
# {"status":"ok"}

curl -s $BFF/readyz
# {"status":"ok"}
```

---

## Unauthenticated request → 401

Any `/api/*` path without a token returns 401:

```bash
curl -s -o /dev/null -w "%{http_code}" $BFF/api/forge/api/v1/venvs
# 401

curl -s -o /dev/null -w "%{http_code}" $BFF/api/weave/api/v1/chains
# 401
```

---

## fusion-forge examples

```bash
# List virtual environments
curl -s -H "Authorization: Bearer $TOKEN" $BFF/api/forge/api/v1/venvs | jq .

# List git builds
curl -s -H "Authorization: Bearer $TOKEN" $BFF/api/forge/api/v1/gitbuilds | jq .
```

The BFF strips `/api/forge` before forwarding — `$BFF/api/forge/api/v1/venvs` reaches forge at `/api/v1/venvs`.

---

## fusion-index examples

```bash
# List artifacts
curl -s -H "Authorization: Bearer $TOKEN" $BFF/api/index/api/v1/artifacts | jq .

# Get a specific artifact
curl -s -H "Authorization: Bearer $TOKEN" $BFF/api/index/api/v1/artifacts/my-artifact | jq .
```

---

## fusion-weave examples

fusion-weave exposes a REST API for managing job DAG resources. All routes live under `/api/v1/` on the weave-api pod; via the BFF they are accessed at `/api/weave/api/v1/`.

```bash
# List WeaveChains
curl -s -H "Authorization: Bearer $TOKEN" $BFF/api/weave/api/v1/chains | jq '.items[].metadata.name'

# Get a specific WeaveChain
curl -s -H "Authorization: Bearer $TOKEN" $BFF/api/weave/api/v1/chains/deploy-demo | jq .

# List WeaveRuns
curl -s -H "Authorization: Bearer $TOKEN" $BFF/api/weave/api/v1/runs | jq '.items[] | {name: .metadata.name, phase: .status.phase}'

# List WeaveJobTemplates
curl -s -H "Authorization: Bearer $TOKEN" $BFF/api/weave/api/v1/jobtemplates | jq '.items[].metadata.name'

# List WeaveTriggers
curl -s -H "Authorization: Bearer $TOKEN" $BFF/api/weave/api/v1/triggers | jq .

# Create a WeaveChain (POST)
curl -s -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  $BFF/api/weave/api/v1/chains \
  -d '{"metadata":{"name":"my-chain"},"spec":{...}}' | jq .

# Delete a WeaveChain (requires admin role on the BFF SA)
curl -s -X DELETE \
  -H "Authorization: Bearer $TOKEN" \
  $BFF/api/weave/api/v1/chains/my-chain
```

The BFF SA has `fusion-platform.io/role: admin`, so all CRUD operations including DELETE are permitted.

---

## Inspect forwarded headers (debug with a mock upstream)

Start a request-echo server in one terminal:

```bash
python3 -c "
import http.server, json
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        self.wfile.write(json.dumps(dict(self.headers)).encode())
    def log_message(self, *a): pass
http.server.HTTPServer(('', 9999), H).serve_forever()
"
```

Set `FORGE_URL=http://localhost:9999` (or `WEAVE_URL=http://localhost:9999`) in `.env`, restart the BFF, then:

```bash
curl -s -H "Authorization: Bearer $TOKEN" $BFF/api/forge/anything | jq .
# or
curl -s -H "Authorization: Bearer $TOKEN" $BFF/api/weave/api/v1/chains | jq .
```

Expected output (abbreviated):

```json
{
  "Authorization": "Bearer <K8s-SA-token>",
  "X-User-Id": "f3a7c...keycloak-sub-uuid",
  "X-User-Email": "testuser@example.com",
  "X-Request-Id": "01J..."
}
```

The original OIDC JWT is **not** forwarded. `Authorization` is replaced by the BFF's SA token. Note that forge/index and weave receive **different** SA tokens.

---

## Denied user → 403

```bash
DENIED_TOKEN=$(curl -s -X POST \
  "$KEYCLOAK_URL/realms/$REALM/protocol/openid-connect/token" \
  -d "grant_type=password&client_id=$CLIENT_ID&client_secret=fusion-gui-secret&username=denieduser&password=password" \
  | jq -r .access_token)

curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $DENIED_TOKEN" \
  $BFF/api/weave/api/v1/chains
# 403
```

---

## Path stripping verification

The BFF strips the `/api/<service>` prefix before forwarding:

| BFF path | Upstream receives |
|---|---|
| `/api/forge/api/v1/venvs` | `/api/v1/venvs` on fusion-forge:8080 |
| `/api/index/api/v1/artifacts` | `/api/v1/artifacts` on fusion-index-backend:8080 |
| `/api/weave/api/v1/chains` | `/api/v1/chains` on fusion-weave-api:8082 |

---

## Token claim inspection (debugging)

Decode the JWT payload without verifying the signature (for debugging only):

```bash
echo $TOKEN | cut -d. -f2 | base64 -d 2>/dev/null | jq '{sub, email, aud, iss, exp}'
```

Example output:

```json
{
  "sub": "f3a7c891-4d2e-4f3a-8c1b-9d0e2f3a4b5c",
  "email": "testuser@example.com",
  "aud": ["fusion-gui", "account"],
  "iss": "http://keycloak.default.svc.cluster.local:8080/realms/fusion",
  "exp": 1745000000
}
```

Key checks:
- `aud` must contain `fusion-gui` (matching `OIDC_CLIENT_ID`) — if missing, add an audience mapper to the Keycloak client (see [INSTALL.md](INSTALL.md#keycloak-setup-minikube-example))
- `iss` must match the BFF's `OIDC_ISSUER_URL` exactly — in minikube, fetch the token from inside the cluster (see above)
- `exp` must be in the future — Keycloak's default access token TTL is 5 minutes
