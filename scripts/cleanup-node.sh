#!/usr/bin/env bash

set -euo pipefail

target="${SRL_TARGET:-}"
username="${SRL_USERNAME:-admin}"
password="${SRL_PASSWORD:-NokiaSrl1!}"
filter_name="${ACL_FILTER_NAME:-cpm}"
sequence_id="${ACL_SEQUENCE_ID:-666}"
image="${GNMIC_IMAGE:-ghcr.io/openconfig/gnmic:latest}"

usage() {
    cat <<'EOF'
Usage: cleanup-node.sh --target HOST:PORT [options]

Remove the IPv4 and IPv6 CPM ACL entries created by setup-node.sh
(<sequence-id> and <sequence-id>+1). The shared ACL filters, their
control-plane bindings, the NDK server, and grpc-server prometheus-exporter
are not removed.

Options:
  -a, --target HOST:PORT       SR Linux gNMI address (required)
  -u, --username USER          gNMI username (default: admin)
  -p, --password PASSWORD      gNMI password (default: NokiaSrl1!)
      --filter-name NAME       IPv4/IPv6 ACL filter name (default: cpm)
      --sequence-id ID         ACL entry sequence ID (default: 666)
      --image IMAGE            gnmic container image
                              (default: ghcr.io/openconfig/gnmic:latest)
  -h, --help                   Show this help

The same values can be set with SRL_TARGET, SRL_USERNAME, SRL_PASSWORD,
ACL_FILTER_NAME, ACL_SEQUENCE_ID, and GNMIC_IMAGE.
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
        --filter-name)
            filter_name="${2:?missing filter name}"
            shift 2
            ;;
        --sequence-id)
            sequence_id="${2:?missing sequence ID}"
            shift 2
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
if [[ ! "$sequence_id" =~ ^[0-9]+$ ]] || ((sequence_id > 65534)); then
    echo "sequence ID must be between 0 and 65534" >&2
    exit 2
fi

acl="/acl/acl-filter[name=${filter_name}]"
consul_seq=$((sequence_id + 1))

docker run --rm --network host "$image" \
    --address "$target" \
    --username "$username" \
    --password "$password" \
    --skip-verify \
    --encoding json_ietf \
    --timeout 2m \
    set \
    --delete "${acl}[type=ipv4]/entry[sequence-id=${sequence_id}]" \
    --delete "${acl}[type=ipv6]/entry[sequence-id=${sequence_id}]" \
    --delete "${acl}[type=ipv4]/entry[sequence-id=${consul_seq}]" \
    --delete "${acl}[type=ipv6]/entry[sequence-id=${consul_seq}]"
