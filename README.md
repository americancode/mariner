                      /-----\
                     /       \
                    /   /-\   \
                    |  |   |  |
                    |   \-/   |
                    |_________|
                       |   |
                       |   |
                       |   |
                       |   |
                       |   |
              ~~~~~~~~~~~~~~~~~~~~~~~
          ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~

# Periscope

Periscope is a small S3 file explorer with Go as the backend. Browser requests never receive S3 credentials and all S3 operations are proxied by the server.

The codebase is split into a Go API service under `cmd/periscope` and `internal/` packages, plus a React/TypeScript frontend under `frontend`.

## Run

```sh
export DATABASE_DRIVER=sqlite
export OIDC_ISSUER=https://idp.example.com/realms/main
export OIDC_CLIENT_ID=periscope
export OIDC_CLIENT_SECRET=...
export OIDC_REDIRECT_URL=http://localhost:8080/auth/callback
export COOKIE_SECRET=$(openssl rand -hex 32)
go run ./cmd/periscope
```

For frontend development, run `cd frontend && npm install && npm run dev`. The production container builds the frontend with Vite and serves it from `/web`.

The OIDC client needs the `openid`, `profile`, and `email` scopes and the callback URL above. `DATA_DIR` defaults to `./data`; it contains the SQLite database `periscope.db`, with encrypted vault envelopes keyed by user. Back it up with appropriate permissions. On first sign-in, choose a master password of at least 10 characters. The server cannot recover it.

### Database and audit storage

PostgreSQL is the default storage backend in the Helm chart. Set `database.driver: sqlite` for lightweight single-replica deployments. PostgreSQL requires either `DATABASE_URL` or the chart's field-based Secret selectors. Both schemas store the same encrypted vault envelope, so the master password is never stored by periscope. The Helm chart supports `database.driver`, `database.url`, and `database.existingSecret`.

The application Helm chart does not deploy a PostgreSQL server; it consumes an external PostgreSQL endpoint or Secret. The local Kustomize environment deploys separate PostgreSQL instances for Keycloak in `infra` and periscope in `periscope`.

For a Secret with individually named fields:

```yaml
database:
  driver: postgres
  existingSecret:
    name: periscope-database
    hostKey: hostname
    portKey: port
    databaseKey: dbname
    usernameKey: user
    passwordKey: pass
  # PostgreSQL TLS mode is configured in Helm values, not in the Secret.
  sslMode: disable
```

Alternatively, set `existingSecret.urlKey` to use one Secret field containing a complete PostgreSQL URL.

Every audit event is serialized once as canonical JSON and stored byte-for-byte in the selected database's `audit_events.event_json` column. The optional audit sidecar polls that table and writes each new `event_json` value unchanged to stdout for Alloy/Loki. This keeps SQLite and PostgreSQL audit content identical without requiring a shared audit-log file or PVC.

The chart sidecar polls the configured database and forwards new audit rows to stdout for Alloy/Loki:

```yaml
audit:
  enabled: true
  sidecar:
    enabled: true
    image: ""
```

The sidecar is enabled by default in the chart. It starts at the latest audit
row and emits only newly committed rows, so a restart does not replay history.
Set `audit.sidecar.enabled: false` when another collector reads the database.
The sidecar is vendor-neutral and supports custom `image`, `command`, `args`,
`env`, and `envFrom` values. This allows a custom Splunk HEC shipper image to
poll or forward events directly:

```yaml
audit:
  sidecar:
    image: registry.example.com/audit-to-splunk:1.0.0
    command: ["/bin/audit-to-splunk"]
    args: ["--database"]
    env:
      - name: SPLUNK_HEC_URL
        valueFrom:
          secretKeyRef:
            name: splunk-hec
            key: url
    envFrom:
      - secretRef:
          name: splunk-hec
```

Keep HEC URLs and tokens in Secret references; do not put them in Helm values.

The chart also supports standard pod volume hooks for custom sidecar or
application configuration:

```yaml
extraVolumes:
  - name: splunk-ca
    secret:
      secretName: splunk-ca
extraVolumeMounts:
  - name: splunk-ca
    mountPath: /etc/splunk-ca
    readOnly: true
```

These mounts are added to the periscope container. The audit sidecar already
shares `/data`; custom sidecar-specific mounts can be added in a future
sidecar-specific hook if required by the shipper image.

Audit events use `log_type=audit` at the ingestion boundary and must not contain passwords, S3 secrets, JWTs, cookies, or object contents. Successful object upload, replace, and delete events include the SHA-256 digest of the exact object bytes. PostgreSQL is the durable audit record; Loki is the searchable operational copy. PostgreSQL-backed deployments do not require the application PVC; enable `persistence.enabled` when using SQLite so its vault survives restarts.

#### Audit administration

Authenticated users in the OIDC group configured by `audit.adminGroup`
(default `admins`) can open `https://periscope.example.com/admin`. The page
loads audit events through `/api/admin/audit`; user, bucket, action, and date
filters are applied by SQL before the paginated results are returned. The UI
does not download the complete audit table for client-side filtering.

Connections support AWS S3, S3-compatible endpoints, static access keys, and the pod/runtime credential chain. In production, put the server behind TLS and set `Secure` on cookies in `main.go` or terminate TLS in a trusted ingress.

## Kubernetes

The Helm chart is in `deploy/helm/periscope`. Build and push the image, then install with OIDC settings:

```sh
docker build -t ghcr.io/your-org/periscope:latest .
docker push ghcr.io/your-org/periscope:latest

helm upgrade --install periscope deploy/helm/periscope \
  --set image.repository=ghcr.io/your-org/periscope \
  --set oidc.issuer=https://idp.example.com/realms/main \
  --set oidc.clientId=periscope \
  --set oidc.clientSecret='...' \
  --set oidc.redirectUrl=https://periscope.example.com/auth/callback \
  --set ingress.enabled=true \
  --set ingress.hosts[0].host=periscope.example.com
```

For production, use `existingSecret` instead of passing OIDC credentials through Helm values. Configure it as a map with `name`, `clientIdKey`, and `clientSecretKey`; the referenced Secret must also contain `COOKIE_SECRET`:

```yaml
existingSecret:
  name: oidc-secret
  clientIdKey: clientId
  clientSecretKey: clientSecret
```

An empty object or an empty `name` makes the chart create its release Secret and use `oidc.clientId`/`oidc.clientSecret`. Keep `replicaCount: 1` while using SQLite and a `ReadWriteOnce` volume.

### Organizations and predefined connections

periscope supports administrator-defined organizations. An organization is a logical collection of users and shared S3 connections. Membership is derived from the claim configured by `oidc.groupsClaim` in the validated OIDC ID token. It defaults to `groups`, but can be set to claims such as `roles`, `memberOf`, or `organization_groups`:

1. The OIDC provider signs an ID token containing values such as `engineering` or `/platform/engineering` under the configured claim.
2. periscope validates the token signature and issuer.
3. periscope compares those group values with each organization's `groups` list.
4. A match grants the user access to that organization's predefined connections.

Matching is exact and case-sensitive. A user only needs one matching group to join an organization. Users can belong to multiple organizations, and connections from all matching organizations are included in their connection list. Use stable organization and connection IDs because changing them changes the generated connection identity.

Organization connections are administrator-managed. Users can browse, upload, delete objects, and create folders according to the permissions of the configured S3 credentials, but they cannot edit or delete the predefined connection from their personal vault. Personal connections remain encrypted per user with the user's master password.

#### Helm values

Configure the organization metadata and connection settings under `organizations`. Both organizations and connections are maps, so their keys are the stable organization and connection names. The optional `id` and `name` fields can override the generated identity or display label. The `credentials` block references a Kubernetes Secret; it does not contain credentials directly:

```yaml
oidc:
  issuer: https://idp.example.com/realms/main
  groupsClaim: groups

organizations:
  engineering:
    name: Engineering
    groups: [engineering, platform]
    connections:
      shared-artifacts:
        name: Shared artifacts
        bucket: engineering-artifacts
        region: us-east-1
        endpoint: https://s3.example.com
        credentials:
          secretName: engineering-artifacts-s3
          accessKeyKey: accessKey
          secretKeyKey: secretKey

extraObjects:
  - apiVersion: external-secrets.io/v1beta1
    kind: ExternalSecret
    metadata:
      name: engineering-artifacts-s3
    spec:
      refreshInterval: 1h
      secretStoreRef:
        name: production
        kind: ClusterSecretStore
      target:
        name: engineering-artifacts-s3
      data:
        - secretKey: accessKey
          remoteRef:
            key: s3/engineering/access-key
        - secretKey: secretKey
          remoteRef:
            key: s3/engineering/secret-key
```

The chart emits organization metadata in `periscope_ORGANIZATIONS_JSON`. For every connection credential reference it also creates two backend-only environment variables using this naming scheme:

```text
periscope_ORG_<ORG_ID>_CONN_<CONNECTION_ID>_ACCESS_KEY
periscope_ORG_<ORG_ID>_CONN_<CONNECTION_ID>_SECRET_KEY
```

Hyphens are converted to underscores and names are uppercased. The application uses these variables to resolve the Secret values at startup. The values are never included in `periscope_ORGANIZATIONS_JSON`, ConfigMap output, API responses, or browser state.

The Secret must exist in the periscope release namespace and contain the keys named by `accessKeyKey` and `secretKeyKey` (default names are `accessKey` and `secretKey`). If an ExternalSecret creates the Secret, install or sync it before starting periscope. Secret rotation requires the pod to restart because credentials are loaded from environment variables at process startup.

The chart also manages the organization encryption key. By default it creates a release-scoped Secret named `<release>-periscope-org-encryption`, using the `encryptionKey` key, and marks it `helm.sh/resource-policy: keep`. Helm generates the value only when the Secret does not already exist; upgrades and uninstall/reinstall operations preserve it. Rotate it only by deliberately deleting the Secret and redeploying, because changing the key can make encrypted organization data unreadable.

To use a Secret managed by ExternalSecrets or another platform, set `organizationEncryption.existingSecret.name` and `organizationEncryption.existingSecret.key`. Helm will then reference the Secret without creating or owning it:

```yaml
organizationEncryption:
  enabled: true
  existingSecret:
    name: periscope-org-encryption
    key: encryptionKey
```

The selected key is injected only into the backend as `periscope_ORG_ENCRYPTION_KEY`; it is not placed in a ConfigMap or exposed to the browser.

#### Security and resources

The chart is configured for the Kubernetes Pod Security Admission `restricted` profile. The distroless image runs as non-root, drops Linux capabilities, disables privilege escalation, uses the `RuntimeDefault` seccomp profile, and has a read-only root filesystem. Application writes are limited to `/data` and `/tmp`. Adjust CPU and memory sizing with `resources.requests` and `resources.limits`:

```yaml
resources:
  requests:
    cpu: 10m
    memory: 32Mi
  limits:
    cpu: 500m
    memory: 256Mi
```

#### Keycloak group claim setup

For Keycloak, add a client-scoped **Groups Membership** protocol mapper to the periscope client:

- Token claim name: the value of `oidc.groupsClaim` (default: `groups`)
- Add to ID token: enabled
- Add to access token: enabled if downstream APIs need it
- Add to userinfo: optional
- Full group path: choose consistently with the values in Helm

Assign users to the matching Keycloak groups, then sign out and sign back in to obtain a fresh ID token. The default OIDC scopes remain `openid profile email`; the mapper adds the configured claim as a claim in the ID token.

#### Example install

```sh
helm upgrade --install periscope deploy/helm/periscope \
  --namespace periscope --create-namespace \
  -f values-production.yaml
```

Before installing, verify that each `credentials.secretName` will be created in the `periscope` namespace:

```sh
kubectl -n periscope get secret engineering-artifacts-s3
kubectl -n periscope rollout restart deployment/periscope
```

To troubleshoot membership, inspect the decoded ID token in a controlled development environment and confirm the exact values under the configured claim, then compare them with the rendered Helm configuration:

```sh
helm template periscope deploy/helm/periscope -f values-production.yaml
```

Do not log, render, or commit Secret data. Helm values may safely contain organization metadata and Secret names, but must never contain raw S3 access keys or secret keys.
