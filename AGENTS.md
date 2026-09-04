# Agent guidance

## Project

Mariner is a Go S3 file explorer with a React/Vite frontend. The Go server owns OIDC login, sessions, the encrypted SQLite vault, and all S3 operations. Browser clients never receive S3 credentials.

- Backend entrypoint: `cmd/mariner`
- Backend packages: `internal/auth`, `internal/config`, `internal/httpapi`, `internal/s3`, `internal/vault`
- Frontend: `frontend/src`
- Production image: `Dockerfile`
- Application Helm chart: `deploy/helm/mariner`
- Local supporting services: `deploy/kustomize/local`
- kind port mapping: `deploy/kind/config.yaml`

## Local cluster

The expected local cluster is Podman-backed kind named `mariner`:

```sh
kind create cluster --config deploy/kind/config.yaml --wait 5m
```

The kind control-plane maps host ports 80 and 443 to NodePorts 30080 and 30443. This requires the Podman VM setting `net.ipv4.ip_unprivileged_port_start=80`; rootless Podman otherwise cannot bind privileged ports.

Traefik is installed with Helm. cert-manager is installed with Helm. Keycloak, PostgreSQL, and MinIO are applied with Kustomize. Mariner itself is installed with Helm.

## Deploy

Build and load the local image:

```sh
podman build -t localhost/mariner:local .
podman save -o /tmp/mariner-local.tar localhost/mariner:local
kind load image-archive /tmp/mariner-local.tar --name mariner
```

Apply supporting services:

```sh
kubectl apply -k deploy/kustomize/local
```

The Kustomize overlay provisions a local Keycloak realm named `mariner`, the `mariner` OIDC client, a demo user, PostgreSQL, MinIO, a self-signed CA, and an sslip.io TLS certificate.

Mariner must use the HTTPS issuer and callback:

- Issuer: `https://keycloak.127.0.0.1.sslip.io/realms/mariner`
- Callback: `https://mariner.127.0.0.1.sslip.io/auth/callback`
- CA trust secret in the Mariner namespace: `sslip-io-ca`. The chart mounts
  custom CA files directly into the Mariner container; the application
  appends them to the platform system CA pool, so public roots remain trusted
  and the combined pool is used for both OIDC and S3 TLS. Configure it with top-level
  `caBundle.secretName` and/or `caBundle.configMapName`; every regular file
  in those objects is appended regardless of filename.

OIDC credentials may come from an existing Secret using the map-shaped
`existingSecret` value. Set `name`, `clientIdKey`, `clientSecretKey`, and
`cookieSecretKey`; the Secret must contain all selected keys. The defaults are
`OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, and `COOKIE_SECRET`. An empty object or empty `name` uses
the chart-managed Secret and `oidc.clientId`/`oidc.clientSecret` values.

OIDC provider logout is enabled by default under `oidc.logout.enabled`; set it
to false for local-only Mariner logout.

OIDC credentials may come from an existing Secret using the map-shaped
`existingSecret` value. Set `name`, `clientIdKey`, `clientSecretKey`, and
`cookieSecretKey`; the Secret must contain all selected keys. The defaults are
`OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, and `COOKIE_SECRET`. An empty object or empty `name` uses
the chart-managed Secret and `oidc.clientId`/`oidc.clientSecret` values.

Use `helm upgrade --install` with `image.repository=localhost/mariner`, `image.tag=local`, `image.pullPolicy=Never`, the HTTPS OIDC values, `caBundle.secretName=sslip-io-ca`, and the Traefik service ClusterIP as a host alias for `keycloak.127.0.0.1.sslip.io`.

## Local credentials

These credentials are development-only and must not be reused in production:

- Keycloak admin: `admin` / `MarinerAdmin123!`
- Keycloak realm user: `demo` / `DemoPassword123!`
- OIDC client: `mariner` / `MarinerClientSecret123!`
- MinIO root: `minioadmin` / `MinioAdmin123!`
- PostgreSQL: database `bitnami_keycloak`, user `bn_keycloak`, password `Postgres123!`

Keycloak users are realm-specific. The demo user is in the `mariner` realm, not `master`.

## Organizations

The Helm chart supports `oidc.groupsClaim`, `organizations`, and `extraObjects`. `oidc.groupsClaim` defaults to `groups` and controls which JWT claim is read; it may be any claim containing a string or string array. Organizations are a map keyed by organization name, and each organization's `connections` is a map keyed by connection name. Each entry declares a configurable ID, display name, matching claim values, and predefined connection details. Each connection references a Kubernetes Secret through `credentials.secretName`, `accessKeyKey`, and `secretKeyKey`; ExternalSecrets can be supplied through `extraObjects`. The chart injects credentials only into the backend pod and emits map-shaped organization metadata as `MARINER_ORGANIZATIONS_JSON`. Do not put raw S3 credentials in Helm values or ConfigMaps.

## Database and audit

The chart defaults to PostgreSQL. SQLite is available for lightweight
single-replica use with `database.driver: sqlite`. PostgreSQL may use `DATABASE_URL` or the chart's
field-based `database.existingSecret` selectors. Both backends use the same encrypted vault
envelope schema and the same `audit_events.event_json` JSON content.

Audit events are serialized once and stored identically in the selected
database's `audit_events.event_json` column. The chart enables `audit.sidecar`
by default; it runs a restricted database-polling sidecar that emits new event
JSON to stdout for Alloy/Loki. Never include passwords,
S3 credentials, JWTs, cookies, or object contents in audit events.

## Validation

Verify all pods and certificates:

```sh
kubectl get pods -A
kubectl -n infra get certificate
curl -ksS https://keycloak.127.0.0.1.sslip.io/realms/mariner/.well-known/openid-configuration
curl -ksS https://mariner.127.0.0.1.sslip.io/api/me
```

The login endpoint should return a redirect to Keycloak. MinIO S3 can be smoke-tested from inside the cluster with the `quay.io/minio/mc` image by creating `mariner`, uploading an object, and running `mc stat`.

## Frontend conventions

The unlock UI must keep the master password masked by default and provide an accessible eye reveal/hide button. Keep UI pieces in reusable, formatted React components. Run:

```sh
cd frontend
npx prettier@3 --write src/App.tsx src/styles.css
npm run build
```

The frontend build is copied into the production image, so frontend changes require rebuilding and reloading the image into kind.

The production image and chart must remain compatible with Kubernetes Pod
Security Admission `restricted`: the image runs as the distroless non-root
user, and the chart uses `runAsNonRoot`, non-zero UID/GID, `RuntimeDefault`
seccomp, dropped capabilities, disabled privilege escalation, and a read-only
root filesystem. Writable application data must use the configured `/data`
PVC and `/tmp` emptyDir. Configure CPU and memory under `resources.requests`
and `resources.limits`; do not remove the restricted security settings to solve
sizing or filesystem issues.

Every new button, link-like action, form control, feature, or modal must match
the existing Mariner visual language. Reuse the established design tokens and
component classes in `frontend/src/styles.css` for colors, typography, borders,
radius, spacing, hover states, focus-visible states, disabled states, and
danger actions. Do not introduce browser-default controls or one-off styling.
New UI should be implemented as a reusable component when it has behavior or
visual treatment that may recur, remain keyboard accessible, and be checked at
mobile widths. Before handoff, verify that the new control looks consistent
with the sidebar, workspace, and existing modal patterns in both its normal and
interactive states.

## Safety and production caveats

- Do not use the local credentials or self-signed CA outside development.
- SQLite uses a single replica and a ReadWriteOnce PVC; do not scale Mariner horizontally without changing storage/session design.
- The local PostgreSQL and MinIO manifests use ephemeral storage.
- Avoid deleting the Mariner PVC during troubleshooting; it contains the encrypted vault.
- Do not use `kubectl port-forward` as a substitute for the configured host 80/443 mappings unless diagnosing ingress.
