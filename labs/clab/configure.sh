#!/bin/bash

set -euo pipefail

source "$(cd -- "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd -P)/common.sh"

usage() {
    cat <<'EOF'
Usage: configure.sh

Configure the SR Linux nodes in a lab that is already running.

Runs scripts/setup-node.sh on each node with Consul registration, remote
write to the lab Prometheus, the interfaces and subinterfaces metrics, and
admin-state enable. Then applies config/interfaces/vars.yaml.

  -h, --help  Show this help
EOF
}

case "${1:-}" in
    "" ) ;;
    -h|--help|help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
esac

cd "$lab_dir"
lab_proxy_env

ip_of() {
    docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}' "clab-${lab_name}-$1" | awk '{print $1}'
}

consul_ip=$(ip_of consul-agent)
prometheus_ip=$(ip_of prometheus)
for node in srl1 srl2; do
    "$lab_dir/../../scripts/setup-node.sh" \
        --target "$(ip_of "$node"):57400" \
        --username "$username" \
        --password "$password" \
        --consul "${consul_ip}:8500" \
        --remote-write "http://${prometheus_ip}:9090/api/v1/write" \
        --metric interfaces \
        --metric subinterfaces \
        --enable
done

nodes="clab-${lab_name}-srl1,clab-${lab_name}-srl2"
gnmic -u "$username" -p "$password" -a "$nodes" --skip-verify --encoding json_ietf --timeout 2m \
    set --request-file config/interfaces/template.gotmpl --request-vars config/interfaces/vars.yaml

for node in srl1 srl2; do
    echo "$node: $(curl -fsS "clab-${lab_name}-${node}:8888/metrics" | grep -vc '^#') samples"
done
