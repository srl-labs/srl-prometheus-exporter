# Containerlab

Two SR Linux 26.7.2 nodes, plus Prometheus, Consul, and Grafana. The same Prometheus, Consul, and Grafana stack can also monitor a hardware node.

Both paths configure SR Linux with [`scripts/setup-node.sh`](../../scripts/setup-node.sh), so a lab node and a hardware node end up with the same config: NDK server, grpc-server `prometheus-exporter`, CPM ACL entries for the exporter port and Consul replies, Consul registration, metrics, and `admin-state enable`.

Requires Docker, containerlab, and gnmic on the host. Host ports: srl1 metrics `8888`, srl2 metrics `8889`, Prometheus `9095`, Grafana `3000`, Consul `8500`.

If `http_proxy`, `https_proxy`, or `ftp_proxy` is set (uppercase names work too), `run.sh` passes them into the lab containers and adds the lab node names to `no_proxy`.

## Lab

```bash
./run.sh build
./stop.sh
```

`run.sh build` builds the deb from this tree, deploys the lab, installs the deb on both nodes, and runs `configure.sh`. `configure.sh` runs `setup-node.sh` against each node, then applies `config/interfaces/vars.yaml`. Run `./configure.sh` on its own to reapply that config.

## Hardware node

1. Start the stack without the SR Linux nodes:

   ```bash
   ./run.sh stack
   ```

2. Install the deb on the node as described in the [repository README](../../README.md#installation).
3. From this host, point the node at the stack's Consul agent:

   ```bash
   ../../scripts/setup-node.sh --target <node>:57400 \
       --consul <this-host-ip>:8500 \
       --metric interfaces --metric subinterfaces --enable
   ```

The node registers its management IP in Consul, and Prometheus scrapes `<node>:8888/metrics`. The node must be able to reach this host on TCP 8500, and this host must reach the node on TCP 8888.

`../../scripts/cleanup-node.sh --target <node>:57400` removes the ACL entries. `./stop.sh` removes the stack. `--help` on any script lists its flags.
