#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NAMESPACE="periscope"
MINIO_NAMESPACE="infra"
MINIO_ENDPOINT="http://minio.infra.svc.cluster.local:9000"

kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -
for bucket in one two three; do
  case "$bucket" in
    one) access_key=org1one-access; secret_key=Org1BucketOne123! ;;
    two) access_key=org1two-access; secret_key=Org1BucketTwo123! ;;
    three) access_key=org1three-access; secret_key=Org1BucketThree123! ;;
  esac
  kubectl -n "$NAMESPACE" create secret generic "org1-bucket-${bucket}-s3" \
    --from-literal=accessKey="$access_key" \
    --from-literal=secretKey="$secret_key" \
    --dry-run=client -o yaml | kubectl apply -f -
done
kubectl -n "$NAMESPACE" create secret generic periscope-bucket-s3 \
  --from-literal=accessKey=periscope-access \
  --from-literal=secretKey=periscopeBucket123! \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl -n "$MINIO_NAMESPACE" run minio-org1-bootstrap --rm -i --restart=Never \
  --image=quay.io/minio/mc:latest --command -- sh -c \
  'mc alias set local "$0" minioadmin MinioAdmin123! >/dev/null && for spec in "periscope:periscope-access:periscopeBucket123!" "org1-bucket-one:org1one-access:Org1BucketOne123!" "org1-bucket-two:org1two-access:Org1BucketTwo123!" "org1-bucket-three:org1three-access:Org1BucketThree123!"; do bucket="${spec%%:*}"; rest="${spec#*:}"; access="${rest%%:*}"; secret="${rest#*:}"; mc mb --ignore-existing "local/$bucket"; printf "{\"Version\":\"2012-10-17\",\"Statement\":[{\"Effect\":\"Allow\",\"Action\":[\"s3:ListBucket\"],\"Resource\":\"arn:aws:s3:::%s\"},{\"Effect\":\"Allow\",\"Action\":[\"s3:*\"],\"Resource\":\"arn:aws:s3:::%s/*\"}]}" "$bucket" "$bucket" >/tmp/$bucket-policy.json; mc admin policy info local "$bucket-policy" >/dev/null 2>&1 || mc admin policy create local "$bucket-policy" /tmp/$bucket-policy.json; mc admin user info local "$access" >/dev/null 2>&1 || mc admin user add local "$access" "$secret"; mc admin policy attach local "$bucket-policy" --user "$access"; done' \
  "$MINIO_ENDPOINT"

# Keycloak realm/client/user/group configuration is idempotent. Use the admin
# REST API from the host so this works with the minimal official image, which
# does not include awk/curl and has limited kcadm output formatting.
KC_BASE="https://keycloak.127.0.0.1.sslip.io"
KC_TOKEN="$(curl -ksSf -X POST "$KC_BASE/realms/master/protocol/openid-connect/token" \
  -d username=admin -d password='periscopeAdmin123!' -d grant_type=password -d client_id=admin-cli \
  | sed -n 's/.*"access_token"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
test -n "$KC_TOKEN"
KC_AUTH=(-H "Authorization: Bearer $KC_TOKEN" -H 'Content-Type: application/json')

client_id="$(curl -ksSf "${KC_AUTH[@]}" "$KC_BASE/admin/realms/periscope/clients?clientId=periscope" | grep -o '"id":"[^"]*"' | head -n1 | cut -d'"' -f4)"
user_id="$(curl -ksSf "${KC_AUTH[@]}" "$KC_BASE/admin/realms/periscope/users?username=demo" | grep -o '"id":"[^"]*"' | head -n1 | cut -d'"' -f4)"
group_id="$(curl -ksSf "${KC_AUTH[@]}" "$KC_BASE/admin/realms/periscope/groups?search=ORG1" | grep -o '"id":"[^"]*"' | head -n1 | cut -d'"' -f4)"
if [ -z "$group_id" ]; then
  curl -ksSf -X POST "${KC_AUTH[@]}" "$KC_BASE/admin/realms/periscope/groups" \
    -d '{"name":"ORG1"}' >/dev/null
  group_id="$(curl -ksSf "${KC_AUTH[@]}" "$KC_BASE/admin/realms/periscope/groups?search=ORG1" | grep -o '"id":"[^"]*"' | head -n1 | cut -d'"' -f4)"
fi
curl -ksSf -X PUT "${KC_AUTH[@]}" \
  "$KC_BASE/admin/realms/periscope/users/$user_id/groups/$group_id" >/dev/null

mapper_url="$KC_BASE/admin/realms/periscope/clients/$client_id/protocol-mappers/models"
if ! curl -ksSf "${KC_AUTH[@]}" "$mapper_url" | grep -q '"name"[[:space:]]*:[[:space:]]*"groups"'; then
  curl -ksSf -X POST "${KC_AUTH[@]}" "$mapper_url" \
    -d '{"name":"groups","protocol":"openid-connect","protocolMapper":"oidc-group-membership-mapper","config":{"full.path":"false","claim.name":"groups","id.token.claim":"true","access.token.claim":"true","userinfo.token.claim":"true"}}' >/dev/null
fi

"$ROOT_DIR/scripts/build-image.sh"
TRAEFIK_IP="$(kubectl -n infra get svc traefik -o jsonpath='{.spec.clusterIP}')"
helm upgrade --install periscope "$ROOT_DIR/deploy/helm/periscope" \
  --namespace "$NAMESPACE" \
  -f "$ROOT_DIR/deploy/helm/periscope/values-local-org1.yaml" \
  --set "hostAliases[0].ip=$TRAEFIK_IP" \
  --set 'hostAliases[0].hostnames[0]=keycloak.127.0.0.1.sslip.io' \
  --server-side=false \
  --wait --timeout 10m
