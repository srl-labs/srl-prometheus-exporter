#!/usr/bin/env bash

set -euo pipefail

target="${SRL_TARGET:-}"
username="${SRL_USERNAME:-admin}"
password="${SRL_PASSWORD:-NokiaSrl1!}"
port="${PROMETHEUS_PORT:-8888}"
filter_name="${ACL_FILTER_NAME:-cpm}"
sequence_id="${ACL_SEQUENCE_ID:-666}"
image="${GNMIC_IMAGE:-ghcr.io/openconfig/gnmic:latest}"
consul="${CONSUL_ADDRESS:-}"
remote_write="${REMOTE_WRITE_URL:-}"
metrics=()
enable=false

usage() {
    cat <<'EOF'
Usage: setup-node.sh --target HOST:PORT [options]

Configure an SR Linux node for the Prometheus exporter in one gNMI commit:

  - system ndk-server admin-state enable
  - grpc-server prometheus-exporter: gnmi, unix socket, no authentication
  - CPM ACL entry <sequence-id> accepting TCP to the exporter port (IPv4, IPv6)
  - both ACL filters bound under system control-plane-traffic input

With --consul, it also adds ACL entry <sequence-id>+1 accepting replies from the
Consul port and sets the exporter registration address. With --remote-write,
it adds entry <sequence-id>+2 for replies from that URL's port and enables
remote-write. Safe to run again.

Options:
  -a, --target HOST:PORT       SR Linux gNMI address (required)
  -u, --username USER          gNMI username (default: admin)
  -p, --password PASSWORD      gNMI password (default: NokiaSrl1!)
      --port PORT              Exporter TCP port (default: 8888)
      --filter-name NAME       IPv4/IPv6 ACL filter name (default: cpm)
      --sequence-id ID         First ACL entry sequence ID (default: 666)
      --consul HOST:PORT       Register the exporter with this Consul agent
      --remote-write URL       POST samples to this remote-write URL
      --metric NAME            Enable a predefined metric; repeat as needed
      --enable                 Set system prometheus-exporter admin-state enable
      --image IMAGE            gnmic container image
                              (default: ghcr.io/openconfig/gnmic:latest)
  -h, --help                   Show this help

The same values can be set with SRL_TARGET, SRL_USERNAME, SRL_PASSWORD,
PROMETHEUS_PORT, ACL_FILTER_NAME, ACL_SEQUENCE_ID, CONSUL_ADDRESS,
REMOTE_WRITE_URL, and GNMIC_IMAGE.
EOF
}

while (($#)); do
    case "$1" in
        -a|--target)
            target="${2:?missing target}"
            shift 2
            ;;
        -u|--username)
            username="${2:?missing username}"
            shift 2
            ;;
        -p|--password)
            password="${2:?missing password}"
            shift 2
            ;;
        --port)
            port="${2:?missing port}"
            shift 2
            ;;
        --filter-name)
            filter_name="${2:?missing filter name}"
            shift 2
            ;;
        --sequence-id)
            sequence_id="${2:?missing sequence ID}"
            shift 2
            ;;
        --consul)
            consul="${2:?missing consul address}"
            shift 2
            ;;
        --remote-write)
            remote_write="${2:?missing remote-write URL}"
            shift 2
            ;;
        --metric)
            metrics+=("${2:?missing metric name}")
            shift 2
            ;;
        --enable)
            enable=true
            shift
            ;;
        --image)
            image="${2:?missing image}"
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "unknown option: $1" >&2
            usage >&2
            exit 2
            ;;
    esac
done

if [[ -z "$target" ]]; then
    echo "--target or SRL_TARGET is required" >&2
    exit 2
fi
if [[ ! "$port" =~ ^[0-9]+$ ]] || ((port < 1 || port > 65535)); then
    echo "port must be between 1 and 65535" >&2
    exit 2
fi
if [[ ! "$sequence_id" =~ ^[0-9]+$ ]] || ((sequence_id > 65533)); then
    echo "sequence ID must be between 0 and 65533" >&2
    exit 2
fi
consul_port=""
if [[ -n "$consul" ]]; then
    consul_port="${consul##*:}"
    if [[ "$consul_port" == "$consul" || ! "$consul_port" =~ ^[0-9]+$ ]] || ((consul_port < 1 || consul_port > 65535)); then
        echo "--consul must be HOST:PORT" >&2
        exit 2
    fi
fi
remote_write_port=""
if [[ -n "$remote_write" ]]; then
    rw_hostport="${remote_write#*://}"
    rw_hostport="${rw_hostport%%/*}"
    remote_write_port="${rw_hostport##*:}"
    if [[ "$remote_write_port" == "$rw_hostport" || ! "$remote_write_port" =~ ^[0-9]+$ ]]; then
        echo "--remote-write URL must include a port" >&2
        exit 2
    fi
fi

acl="/acl/acl-filter[name=${filter_name}]"
binding="/system/control-plane-traffic/input/acl/acl-filter[name=${filter_name}]"
exporter=/system/prometheus-exporter

updates=(
    --update-path /system/ndk-server
    --update-value '{"admin-state":"enable"}'
    --update-path "/system/grpc-server[name=prometheus-exporter]"
    --update-value '{"admin-state":"enable","metadata-authentication":false,"services":["srl_nokia-grpc:gnmi"],"unix-socket":{"admin-state":"enable"}}'
    --update-path "${acl}[type=ipv4]/entry[sequence-id=${sequence_id}]"
    --update-value "$(printf '{"match":{"ipv4":{"protocol":"tcp"},"transport":{"destination-port":{"value":%s}}},"action":{"accept":{}}}' "$port")"
    --update-path "${acl}[type=ipv6]/entry[sequence-id=${sequence_id}]"
    --update-value "$(printf '{"match":{"ipv6":{"next-header":"tcp"},"transport":{"destination-port":{"value":%s}}},"action":{"accept":{}}}' "$port")"
    --update-path "${binding}[type=ipv4]"
    --update-value '{}'
    --update-path "${binding}[type=ipv6]"
    --update-value '{}'
    --update-path "${exporter}/scrape"
    --update-value "$(printf '{"admin-state":"enable","port":"%s"}' "$port")"
)

if [[ -n "$consul" ]]; then
    consul_seq=$((sequence_id + 1))
    updates+=(
        --update-path "${acl}[type=ipv4]/entry[sequence-id=${consul_seq}]"
        --update-value "$(printf '{"match":{"ipv4":{"protocol":"tcp"},"transport":{"source-port":{"value":%s}}},"action":{"accept":{}}}' "$consul_port")"
        --update-path "${acl}[type=ipv6]/entry[sequence-id=${consul_seq}]"
        --update-value "$(printf '{"match":{"ipv6":{"next-header":"tcp"},"transport":{"source-port":{"value":%s}}},"action":{"accept":{}}}' "$consul_port")"
        --update-path "${exporter}/registration"
        --update-value "$(printf '{"address":"%s","admin-state":"enable"}' "$consul")"
    )
fi

if [[ -n "$remote_write" ]]; then
    rw_seq=$((sequence_id + 2))
    updates+=(
        --update-path "${acl}[type=ipv4]/entry[sequence-id=${rw_seq}]"
        --update-value "$(printf '{"match":{"ipv4":{"protocol":"tcp"},"transport":{"source-port":{"value":%s}}},"action":{"accept":{}}}' "$remote_write_port")"
        --update-path "${acl}[type=ipv6]/entry[sequence-id=${rw_seq}]"
        --update-value "$(printf '{"match":{"ipv6":{"next-header":"tcp"},"transport":{"source-port":{"value":%s}}},"action":{"accept":{}}}' "$remote_write_port")"
        --update-path "${exporter}/remote-write"
        --update-value "$(printf '{"url":"%s","interval":"15s","admin-state":"enable"}' "$remote_write")"
    )
fi

for metric in "${metrics[@]}"; do
    updates+=(
        --update-path "${exporter}/metric[name=${metric}]/admin-state"
        --update-value enable
    )
done

if [[ "$enable" == true ]]; then
    updates+=(
        --update-path "${exporter}/admin-state"
        --update-value enable
    )
fi

docker run --rm --network host "$image" \
    --address "$target" \
    --username "$username" \
    --password "$password" \
    --skip-verify \
    --encoding json_ietf \
    --timeout 2m \
    set \
    "${updates[@]}"
