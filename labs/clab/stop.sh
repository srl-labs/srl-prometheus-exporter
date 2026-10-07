#!/bin/bash

set -euo pipefail

source "$(cd -- "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd -P)/common.sh"

usage() {
    cat <<'EOF'
Usage: stop.sh

Destroy the prom-exporter containerlab. Does nothing if the lab is not deployed.

  -h, --help  Show this help
EOF
}

case "${1:-}" in
    "" ) ;;
    -h|--help|help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
esac

cd "$lab_dir"
if ! clab_sudo clab destroy -t "$topo" --cleanup; then
    echo "lab $lab_name was not running"
fi
