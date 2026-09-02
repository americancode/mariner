#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TRAEFIK_IP="$(kubectl -n infra get svc traefik -o jsonpath='{.spec.clusterIP}')"

kubectl create namespace periscope --dry-run=client -o yaml | kubectl apply -f -
kubectl -n periscope apply -f "$ROOT_DIR/deploy/kustomize/local/periscope-postgres.yaml"
kubectl apply -k "$ROOT_DIR/deploy/kustomize/local"
kubectl -n periscope wait --for=condition=available deployment/periscope-db --timeout=2m

# The local Kustomize namespace transformer places the bootstrap CA Certificate
# in infra. ClusterIssuer reads its CA keypair from cert-manager, and periscope
# needs a copy in its own namespace.
kubectl -n infra wait --for=condition=Ready certificate/sslip-io-ca --timeout=2m
for namespace in cert-manager periscope; do
  cert="$(kubectl -n infra get secret sslip-io-ca -o jsonpath='{.data.tls\.crt}' | base64 -D)"
  key="$(kubectl -n infra get secret sslip-io-ca -o jsonpath='{.data.tls\.key}' | base64 -D)"
  kubectl -n "$namespace" create secret generic sslip-io-ca \
    --from-literal=tls.crt="$cert" --from-literal=tls.key="$key" \
    --dry-run=client -o yaml | kubectl apply -f -
done
kubectl -n infra wait --for=condition=Ready certificate/sslip-io-tls --timeout=2m

kubectl -n periscope create secret generic periscope-postgres \
  --from-literal=host=periscope-db.periscope.svc.cluster.local \
  --from-literal=port=5432 \
  --from-literal=database=periscope \
  --from-literal=username=periscope \
  --from-literal=password='periscopePostgres123!' \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl -n periscope create secret generic periscope-bucket-s3 \
  --from-literal=accessKey=periscope-access \
  --from-literal=secretKey=periscopeBucket123! \
  --dry-run=client -o yaml | kubectl apply -f -
for bucket in one two three; do
  case "$bucket" in
    one) access_key=org1one-access; secret_key=Org1BucketOne123! ;;
    two) access_key=org1two-access; secret_key=Org1BucketTwo123! ;;
    three) access_key=org1three-access; secret_key=Org1BucketThree123! ;;
  esac
  kubectl -n periscope create secret generic "org1-bucket-${bucket}-s3" \
    --from-literal=accessKey="$access_key" \
    --from-literal=secretKey="$secret_key" \
    --dry-run=client -o yaml | kubectl apply -f -
done

helm upgrade --install periscope "$ROOT_DIR/deploy/helm/periscope" \
  --namespace periscope \
  --values "$ROOT_DIR/deploy/helm/periscope/values-local-org1.yaml" \
  --set database.driver=postgres \
  --set database.existingSecret.name=periscope-postgres \
  --set image.repository=localhost/periscope \
  --set image.tag=local \
  --set image.pullPolicy=Never \
  --set oidc.issuer=https://keycloak.127.0.0.1.sslip.io/realms/periscope \
  --set oidc.clientId=periscope \
  --set oidc.clientSecret='periscopeClientSecret123!' \
  --set oidc.redirectUrl=https://periscope.127.0.0.1.sslip.io/auth/callback \
  --set caBundle.secretName=sslip-io-ca \
  --set cookieSecret='local-cookie-secret-change-me-1234567890' \
  --set ingress.enabled=true \
  --set ingress.className=traefik \
  --set 'ingress.hosts[0].host=periscope.127.0.0.1.sslip.io' \
  --set 'ingress.hosts[0].paths[0].path=/' \
  --set 'ingress.hosts[0].paths[0].pathType=Prefix' \
  --set 'ingress.tls[0].secretName=sslip-io-tls' \
  --set 'ingress.tls[0].hosts[0]=periscope.127.0.0.1.sslip.io' \
  --set "hostAliases[0].ip=$TRAEFIK_IP" \
  --set 'hostAliases[0].hostnames[0]=keycloak.127.0.0.1.sslip.io' \
  --server-side=false \
  --wait --timeout 10m
