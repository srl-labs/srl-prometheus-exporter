# Shared by run.sh, configure.sh, and stop.sh. Not meant to be executed.

lab_dir="$(cd -- "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd -P)"
topo="$lab_dir/prometheus-exporter.clab.yaml"
lab_name=prom-exporter
version=0.3.0
username=admin
password='NokiaSrl1!'
deb_name="srl-prometheus-exporter_${version}_Linux_x86_64.deb"

# Container names containerlab derives from the lab and node names.
lab_nodes="clab-${lab_name}-srl1,clab-${lab_name}-srl2,clab-${lab_name}-prometheus,clab-${lab_name}-consul-agent,clab-${lab_name}-grafana"

# Copy host proxy variables into the process environment clab reads.
# When a proxy is set, keep lab node names off it so in-lab traffic stays local.
lab_proxy_env() {
    export http_proxy="${http_proxy:-${HTTP_PROXY:-}}"
    export https_proxy="${https_proxy:-${HTTPS_PROXY:-}}"
    export ftp_proxy="${ftp_proxy:-${FTP_PROXY:-}}"
    export HTTP_PROXY="${HTTP_PROXY:-${http_proxy:-}}"
    export HTTPS_PROXY="${HTTPS_PROXY:-${https_proxy:-}}"
    export FTP_PROXY="${FTP_PROXY:-${ftp_proxy:-}}"

    local base="${no_proxy:-${NO_PROXY:-}}"
    if [[ -n "$http_proxy$https_proxy$ftp_proxy" ]]; then
        export no_proxy="${base:+$base,}$lab_nodes"
        export NO_PROXY="$no_proxy"
    else
        export no_proxy="$base"
        export NO_PROXY="${NO_PROXY:-$base}"
    fi
}

# sudo resets the environment from /etc/environment, which drops any no_proxy
# names added above. env runs after that reset and sets what clab expands.
clab_sudo() {
    sudo env \
        "http_proxy=${http_proxy:-}" \
        "https_proxy=${https_proxy:-}" \
        "ftp_proxy=${ftp_proxy:-}" \
        "no_proxy=${no_proxy:-}" \
        "HTTP_PROXY=${HTTP_PROXY:-}" \
        "HTTPS_PROXY=${HTTPS_PROXY:-}" \
        "FTP_PROXY=${FTP_PROXY:-}" \
        "NO_PROXY=${NO_PROXY:-}" \
        "$@"
}
