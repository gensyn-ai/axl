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
| `-listen` | Listen address for incoming peers | `-listen tls://0.0.0.0:9001` |
| `-config` | Path to configuration file | `-config node-config.json` |

Addresses for the MCP router and A2A server can also be configured via `node-config.json`:

```json
{
  "router_addr": "http://127.0.0.1",
  "router_port": 9003,
  "a2a_addr": "http://127.0.0.1",
  "a2a_port": 9004
}
```

If no addresses are configured, the corresponding streams are disabled.

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
- `X-Destination-Peer-Id`: Hex-encoded 32-byte peer ID (ed25519 public key) of destination

**Body:** Raw binary data (or JSON for MCP requests)

**Response:**
- If the remote peer responds: `200 OK` with JSON response body
- If no response (fire-and-forget): `200 OK` with `X-Sent-Bytes` header

### `GET /recv`

Poll for received messages (non-MCP traffic only). MCP messages are automatically routed to the MCP router.

**Response:**
- `204 No Content` if queue is empty
- `200 OK` with raw binary body and `X-From-Peer-Id` header (sender's peer ID)

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
2. The multiplexer checks each registered stream:
   - `"service"` field → MCP request → forwarded to the MCP router
   - `"a2a": true` → A2A request → forwarded to the A2A server
3. The response is sent back to the remote peer over the same TCP connection
4. Unmatched messages are queued and returned via `/recv`

### Stream Multiplexing

Incoming TCP messages are routed by a multiplexer based on their content:

- Messages with a `"service"` field → **MCP stream** → MCP router
- Messages with `"a2a": true` → **A2A stream** → A2A server
- Everything else → generic recv queue (polled via `/recv`)

### MCP Routing

Incoming messages with a `"service"` field are recognized as MCP requests:

```json
{
  "service": "weather",
  "request": {"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": {...}}
}
```

These are forwarded to the MCP router (default `http://127.0.0.1:9003/route`), which routes them to the appropriate registered MCP server.

### A2A Routing

Incoming messages with `"a2a": true` are forwarded to the local A2A server:

```json
{
  "a2a": true,
  "request": {"jsonrpc": "2.0", "method": "message/send", ...}
}
```

The `request` field contains a raw A2A JSON-RPC payload. The A2A server processes it and the response is sent back to the remote peer.

### `POST /a2a/{peer_id}`

Send an A2A request to a remote peer. The request body is a raw A2A JSON-RPC payload, which gets wrapped in a transport envelope, sent over Yggdrasil TCP, and the response is returned.

**Example — list tools via A2A:**
```bash
curl -X POST http://127.0.0.1:9002/a2a/{peer_id} \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "method": "message/send",
    "id": 1,
    "params": {
      "message": {
        "role": "user",
        "parts": [{"kind": "text", "text": "{\"service\":\"weather\",\"request\":{\"jsonrpc\":\"2.0\",\"method\":\"tools/list\",\"id\":1,\"params\":{}}}"}],
        "messageId": "test123"
      }
    }
  }'
```

**Example — call a tool via A2A:**
```bash
curl -X POST http://127.0.0.1:9002/a2a/{peer_id} \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "method": "message/send",
    "id": 1,
    "params": {
      "message": {
        "role": "user",
        "parts": [{"kind": "text", "text": "{\"service\":\"weather\",\"request\":{\"jsonrpc\":\"2.0\",\"method\":\"tools/call\",\"id\":1,\"params\":{\"name\":\"get_weather\",\"arguments\":{\"city\":\"Dublin\"}}}}"}],
        "messageId": "test123"
      }
    }
  }'
```

Replace `{peer_id}` with the hex-encoded public key of the remote peer (64 hex characters). The `messageId` is a client-assigned correlation ID. The text part must be a JSON-stringified MCP request matching the format the A2A server expects.

## Submodules

- **yggdrasil-go**: Official Yggdrasil implementation (https://github.com/yggdrasil-network/yggdrasil-go)
