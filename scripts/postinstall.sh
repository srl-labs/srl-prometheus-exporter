#!/bin/bash

srlinux=/opt/srlinux/bin/sr_linux
srcli=/opt/srlinux/bin/sr_cli

if [[ ! -x "$srlinux" || ! -x "$srcli" ]]; then
    exit 0
fi

appmgr=$("$srlinux" --status | grep app_mgr | cut -d: -f 2 | tr -s ' ')

if [[ $appmgr == "not running" ]]; then
    exit 0
fi

# Set SRL_PROMETHEUS_EXPORTER_SKIP_CONFIG to install the package without
# changing node configuration. Uninstall does not remove either setting.
if [[ -n ${SRL_PROMETHEUS_EXPORTER_SKIP_CONFIG:-} ]]; then
    echo "SRL_PROMETHEUS_EXPORTER_SKIP_CONFIG is set; not enabling the NDK server or adding the gnmi unix socket"
else
    # The NDK server is disabled by default and does not create its unix socket
    # until this commit succeeds. Do this before app_mgr starts the exporter.
    if ! "$srcli" -ec -- system ndk-server admin-state enable; then
        echo "failed to enable system ndk-server" >&2
        exit 1
    fi

    # Unix socket only, no metadata authentication. Any local process that
    # can open the socket can call gNMI.
    if ! "$srcli" -ec -- system grpc-server prometheus-exporter admin-state enable services [ gnmi ] metadata-authentication false unix-socket admin-state enable; then
        echo "failed to configure system grpc-server prometheus-exporter" >&2
        exit 1
    fi

    echo "enabled the NDK server and added grpc-server prometheus-exporter (gnmi service, unix socket sr_grpc_server_prometheus-exporter)"
fi

"$srcli" tools system app-management application app_mgr reload
