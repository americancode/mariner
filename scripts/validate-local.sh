#!/usr/bin/env bash
set -euo pipefail

kubectl wait --for=condition=Ready pod --all -A --timeout=5m
kubectl -n infra get certificate sslip-io-ca sslip-io-tls
curl -ksSf https://keycloak.127.0.0.1.sslip.io/realms/periscope/.well-known/openid-configuration >/dev/null
curl -ksSf https://periscope.127.0.0.1.sslip.io/api/me
curl -ksSf https://minio.127.0.0.1.sslip.io/minio/health/live >/dev/null
curl -ksSf -D - -o /dev/null https://periscope.127.0.0.1.sslip.io/auth/login | grep -qi '^location:.*keycloak.127.0.0.1.sslip.io'
echo "local HTTPS/OIDC/MinIO validation passed"
