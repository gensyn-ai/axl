# AXL

Gensyn is building an open, permissionless, P2P network for decentralized agentic and AI/ML applications.  This repository provides a tool to minimize friction when spinning up P2P networks.  It provides a node as an entrypoint into a decentralized P2P network with an api bridge for simple application interface.  The node provides the communication layer for agents and AI applications to exchange data directly with each other, forgoing any centralized services.  

## Overview

This project builds upon the Yggdrasil network stack with gvisor/tcp to provide a standalone network node with a local HTTP API bridge. It allows applications (e.g., serving MoE inference, AI agents, etc.) to send/receive data to/from other nodes without requiring a system-wide TUN interface or root privileges.

![network cartoon](assets/distributed-agents-cartoon.png)

**Key features:**
- **No TUN required** — runs entirely in userspace using gVisor's network stack
- **No port forwarding needed** — connects outbound to peers; receives data over the same connection
- **Simple HTTP API** — send/recv binary data, query network topology

## Quick Start
### Requirements
- Go 1.25.5+ installed (the build system pins `GOTOOLCHAIN=go1.25.5` automatically)

```bash
make build
openssl genpkey -algorithm ed25519 -out private.pem # or provide your own key
./node -config node-config.json
```

See [Configuration](docs/configuration.md) for build details, CLI flags, and `node-config.json` options.

### Public Nodes
At least one public node is required for spinning up fresh networks. A public node must meet two criteria:
1. If behind a firewall, configure the host machine to expose a port such that the machine is reachable to network traffic.  
2. configure node-config.json to listen on the port


#### Example Config
For example, if you were to run several machines on a LAN in a hub and spoke configuration, you could set the config of the listening machine to 
```json
{
  "PrivateKeyPath": "private.pem",
  "Peers": [
  ],
  "Listen": ["tls://0.0.0.0:9001"]
}
```
With the private nodes peering to the listening node's IP address
```json
{
  "PrivateKeyPath": "private.pem",
  "Peers": [
    "tls://192.168.0.22:9001"
  ],
  "Listen": []
}
```

## Philosophy

Our intent is to provide a simple, permissionless, and secure communication layer for AI/ML workflows.  This node is agnostic to the application layer and simply provides an interface for applications to build upon.  Enforcing the separation of concerns between the network layer and the application layer allows for greater flexibility and scalability.  We are excited to see what you build!

We encourage anyone to run a public node to help bootstrap the network, or just spin up your own P2P network in isolation.

## Documentation

| Document | Contents |
|----------|----------|
| [Architecture](docs/architecture.md) | System diagram, how it works, wire format, submodules |
| [HTTP API](docs/api.md) | All endpoints: `/topology`, `/send`, `/recv`, `/mcp/`, `/a2a/` |
| [Configuration](docs/configuration.md) | Build/run, CLI flags, `node-config.json` |
| [Integrations](docs/integrations.md) | Python services: MCP router, A2A server, test client |
| [Examples](docs/examples.md) | Remote MCP server, adding A2A |

## Citation

If you use AXL in your research or project, please cite it as follows:

**BibTeX:**

```bibtex
@misc{gensyn2026axl,
  title         = {{AXL}: A P2P Network for Decentralized Agentic and {AI/ML} Applications},
  author        = {{Gensyn AI}},
  year          = {2026},
  howpublished  = {\url{[https://github.com/gensyn-ai/axl](https://github.com/gensyn-ai/axl)}},
  note          = {Open-source software}
}
```
