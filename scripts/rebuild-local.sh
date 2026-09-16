#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLUSTER_NAME="mariner"

"$ROOT_DIR/scripts/kind-clear.sh" "$CLUSTER_NAME"
kind create cluster --config "$ROOT_DIR/deploy/kind/config.yaml" --wait 5m
"$ROOT_DIR/scripts/install-platform.sh"
"$ROOT_DIR/scripts/build-image.sh"
"$ROOT_DIR/scripts/deploy-local.sh"

kubectl get pods -A
kubectl -n infra get certificate
kubectl -n mariner rollout status deployment/mariner --timeout=3m
