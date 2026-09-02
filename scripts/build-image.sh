#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="localhost/periscope:local"
ARCHIVE="/tmp/periscope-local.tar"

podman build -t "$IMAGE" "$ROOT_DIR"
podman save -o "$ARCHIVE" "$IMAGE"
kind load image-archive "$ARCHIVE" --name periscope
