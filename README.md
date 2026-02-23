# Gensyn Node

Gensyn is building an open, permissionless, P2P network for decentralized agentic and AI/ML applications.  This repository provides the entrypoint to the Gensyn network.  The node provides the communication layer for agents and AI applications to exchange data directly with each other, forgoing any centralized services.  

## Overview

This project builds upon the Yggdrasil network stack with gvisor/tcp to provide a standalone network node with a local HTTP API bridge. It allows applications (e.g., serving MoE inference, AI agents, etc.) to send/receive data to/from other nodes without requiring a system-wide TUN interface or root privileges.

<img src="assets/distributed-agents-cartoon.png" alt="network cartoon" width="50%">

**Key features:**
- **No TUN required** — runs entirely in userspace using gVisor's network stack
- **No port forwarding needed** — connects outbound to peers; receives data over the same connection
- **Simple HTTP API** — send/recv binary data, query network topology

## Quick Start

```bash
go build -o node ./cmd/node/
openssl genpkey -algorithm ed25519 -out private.pem # or provide your own key
./node -config node-config.json
```

See [Configuration](docs/configuration.md) for build details, CLI flags, and `node-config.json` options.

## Philosophy

Our intent is to provide a simple, permissionless, and secure communication layer for AI/ML workflows.  This node is agnostic to the application layer and simply provides an interface for applications to build upon.  Enforcing the separation of concerns between the network layer and the application layer allows for greater flexibility and scalability.  We are excited to see what you build!

### Public Nodes
We encourage anyone to run a public node to help bootstrap the network.  To run a public node one must expose a public IP address and port, and then run the node with the `-listen` flag or setting the config option.

## Documentation

| Document | Contents |
|----------|----------|
| [Architecture](docs/architecture.md) | System diagram, how it works, wire format, submodules |
| [HTTP API](docs/api.md) | All endpoints: `/topology`, `/send`, `/recv`, `/mcp/`, `/a2a/` |
| [Configuration](docs/configuration.md) | Build/run, CLI flags, `node-config.json` |
| [Integrations](docs/integrations.md) | Python services: MCP router, A2A server, test client |
| [Examples](docs/examples.md) | Remote MCP server, adding A2A |
