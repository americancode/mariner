#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLUSTER_NAME="periscope"

if kind get clusters 2>/dev/null | grep -qx "$CLUSTER_NAME"; then
  echo "kind cluster $CLUSTER_NAME already exists"
  exit 0
fi

kind create cluster --config "$ROOT_DIR/deploy/kind/config.yaml" --wait 5m
