# Yggdrasil Node

A userspace Yggdrasil node with an HTTP API for sending and receiving data over the Yggdrasil mesh network.

## Overview

This project embeds the Yggdrasil network stack in a standalone Go application, exposing a local HTTP API. It allows applications (e.g., Python scripts) to send/receive data to/from other Yggdrasil nodes without requiring a system-wide TUN interface or root privileges.

**Key features:**
- **No TUN required** — runs entirely in userspace using gVisor's network stack
- **No port forwarding needed** — connects outbound to peers; receives data over the same connection
- **Simple HTTP API** — send/recv binary data, query network topology

## Architecture

```
┌────────────────┐       HTTP        ┌─────────────────────────────────────┐
│  Your App      │◄────────────────►│  client.go                          │
│  (Python, etc) │   localhost:9002  │  ┌─────────────┐  ┌──────────────┐  │
└────────────────┘                   │  │ gVisor TCP  │◄►│ Yggdrasil    │  │
                                     │  │ Stack       │  │ Core         │  │
                                     │  └─────────────┘  └──────┬───────┘  │
                                     └─────────────────────────│──────────┘
                                                               │ TLS/TCP
                                                               ▼
                                                      ┌────────────────┐
                                                      │  Public Peer   │
                                                      │  (or LAN peer) │
                                                      └────────────────┘
```

## Setup

### Clone with Submodules
```bash
git clone --recurse-submodules <repo-url>
# Or if already cloned:
git submodule update --init --recursive
```

### Build / Run
```bash
cd client
go run client.go [flags]
```

## Usage

### Command-line Flags

| Flag | Description | Example |
|------|-------------|---------|
| `-peer` | Peer URI to connect to | `-peer tls://1.2.3.4:9001` |
| `-listen` | Listen address for incoming peers | `-listen tls://0.0.0.0:9001` |
| `-router` | MCP router URL | `-router http://127.0.0.1:9003` |

If no flags are provided, connects to a default public peer and routes MCP traffic to `http://127.0.0.1:9003`.

### Examples

**Connect to default peer (client mode):**
```bash
go run client.go
```

**Connect to a specific peer:**
```bash
go run client.go -peer tls://somenode.example.com:9001
```

**Run as a listener (server mode):**
```bash
go run client.go -listen tls://0.0.0.0:9001
```

## HTTP API

The node exposes a local HTTP server on `127.0.0.1:9002`.

### `GET /topology`

Returns node info and peer/tree state.

**Response:**
```json
{
  "our_ipv6": "200:abcd:...",
  "our_public_key": "abcd1234...",
  "peers": [...],
  "tree": [...]
}
```

### `POST /send`

Send data to another node. If the remote node responds (e.g., an MCP request/response), the response is returned directly. Otherwise falls back to a fire-and-forget acknowledgement.

**Headers:**
- `X-Destination-Key`: Hex-encoded 32-byte public key of destination

**Body:** Raw binary data (or JSON for MCP requests)

**Response:**
- If the remote peer responds: `200 OK` with JSON response body
- If no response (fire-and-forget): `200 OK` with `X-Sent-Bytes` header

### `GET /recv`

Poll for received messages (non-MCP traffic only). MCP messages are automatically routed to the MCP router.

**Response:**
- `204 No Content` if queue is empty
- `200 OK` with raw binary body and `X-From-Key` header (sender's public key)

## How It Works

1. **Yggdrasil Core** — Generates a keypair, derives an IPv6 address (`200::/7`), and connects to peers
2. **gVisor Stack** — Provides a userspace TCP/IP stack bound to the Yggdrasil IPv6 address
3. **TCP Listener** — Listens on port 7000 (internal) for incoming messages from other nodes
4. **HTTP Bridge** — Exposes send/recv/topology endpoints on localhost for your application

When you send data, it:
1. Converts the destination public key → Yggdrasil IPv6 address
2. Opens a TCP connection through the gVisor stack
3. Sends a length-prefixed message
4. Waits for a response (with 30s timeout) and returns it to the caller

When you receive data:
1. The TCP listener accepts connections from the overlay
2. If the message has a `"service"` field, it is treated as an MCP request and forwarded to the MCP router via `POST /route`
3. The router's response is sent back to the remote peer over the same TCP connection
4. Non-MCP messages are queued and returned via `/recv`

### MCP Routing

Incoming messages with a `"service"` field are recognized as MCP requests:

```json
{
  "service": "weather",
  "request": {"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {...}}
}
```

These are forwarded to the MCP router (default `http://127.0.0.1:9003/route`), which routes them to the appropriate registered MCP server. See [demcp](./client/) for the router and server implementation.

## Submodules

- **yggdrasil-go**: Official Yggdrasil implementation (https://github.com/yggdrasil-network/yggdrasil-go)
