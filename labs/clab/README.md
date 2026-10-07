# Containerlab

Two SR Linux 26.7.2 nodes with Prometheus, Consul, and Grafana. Needs Docker, containerlab, and gnmic. Proxy variables, if set, are passed to the containers.

Host ports: srl1 `8888`, srl2 `8889`, Prometheus `9095`, Grafana `3000`, Consul `8500`.

## Lab

```bash
./run.sh build   # build the deb, deploy, install, configure
./stop.sh
```

## Real node

```bash
./run.sh stack
../../scripts/setup-node.sh --target <node>:57400 \
    --consul <this-host-ip>:8500 \
    --metric interfaces --metric subinterfaces --enable
```

Install the deb on the node first ([README](../../README.md#installation)). The node must reach this host on TCP 8500, and this host must reach the node on TCP 8888.
