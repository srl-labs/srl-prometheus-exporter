#!/bin/bash

set -euo pipefail

source "$(cd -- "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd -P)/common.sh"

usage() {
    cat <<'EOF'
Usage: run.sh [build|stack]

Deploy the prom-exporter containerlab, install the exporter deb, and configure
the nodes. See README.md for how this differs from a hardware node.

  build       Build the deb from this repo first (make dev)
  stack       Deploy only Prometheus, Consul, and Grafana, for a hardware node
  -h, --help  Show this help

With no argument, run.sh downloads the published v0.3.1 amd64 deb. That asset is not on the release yet; use build.

If http_proxy, https_proxy, or ftp_proxy is set (uppercase names work too),
those values are passed into every lab container. Lab node names are added to
no_proxy so traffic between containers does not go through the proxy.
EOF
}

mode=download
case "${1:-}" in
    "" ) ;;
    build) mode=build ;;
    stack) mode=stack ;;
    -h|--help|help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
esac

"$lab_dir/stop.sh"

if [[ $mode == stack ]]; then
    lab_proxy_env
    cd "$lab_dir"
    clab_sudo clab deploy -t "$topo" --reconfigure --node-filter prometheus,consul-agent,grafana
    exit 0
fi

mkdir -p "$lab_dir/app"
rm -f "$lab_dir/app/"*.deb

if [[ $mode == build ]]; then
    echo "- building packages"
    make -C "$lab_dir/../.." dev
    cp "$lab_dir/../../dist/$deb_name" "$lab_dir/app/"
else
    echo "- downloading $deb_name"
    curl -fL "https://github.com/srl-labs/srl-prometheus-exporter/releases/download/v${version}/${deb_name}" \
        -o "$lab_dir/app/$deb_name"
fi

lab_proxy_env
if [[ -n "$http_proxy$https_proxy" ]]; then
    echo "- using proxy ${https_proxy:-$http_proxy}"
fi

cd "$lab_dir"
clab_sudo clab deploy -t "$topo" --reconfigure

for node in "clab-${lab_name}-srl1" "clab-${lab_name}-srl2"; do
    echo "- installing on $node"
    sudo docker exec "$node" sudo apt-get install -y --reinstall "/tmp/pkg/$deb_name"
done

# Loading the exporter YANG restarts sr_mgmt_server; wait until it answers.
for node in "clab-${lab_name}-srl1" "clab-${lab_name}-srl2"; do
    echo "- waiting for $node management server"
    for _ in $(seq 1 60); do
        if sudo docker exec "$node" sr_cli "info from state system prometheus-exporter oper-state" >/dev/null 2>&1; then
            continue 2
        fi
        sleep 2
    done
    echo "$node management server did not come up; check /var/log/srlinux/stdout/mgmt_server.log" >&2
    exit 1
done

"$lab_dir/configure.sh"
