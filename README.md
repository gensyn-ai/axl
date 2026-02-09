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
│  Your App      │◄────────────────►│  node                               │
│  (Python, etc) │   localhost:9002  │  ┌─────────────┐  ┌──────────────┐  │
└────────────────┘                   │  │ gVisor TCP  │◄►│ Yggdrasil    │  │
                                     │  │ Stack       │  │ Core         │  │
┌────────────────┐                   │  └──────┬──────┘  └──────┬───────┘  │
│  MCP Router    │◄──── stream ──────│─────────┘                │          │
│  A2A Server    │                   └──────────────────────────│──────────┘
└────────────────┘                                              │ TLS/TCP
                                                                ▼
                                                       ┌────────────────┐
                                                       │  Public Peer   │
                                                       │  (or LAN peer) │
                                                       └────────────────┘
```

## Setup

### Build / Run
```bash
go build -o node ./cmd/node/
./node -config node-config.json
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

**Run with default config:**
```bash
./node
```

**Run with a listen address override:**
```bash
./node -listen tls://0.0.0.0:9001
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

Send data to another node (fire-and-forget). Does not wait for a response.

**Headers:**
- `X-Destination-Peer-Id`: Hex-encoded 32-byte peer ID (ed25519 public key) of destination

**Body:** Raw binary data

**Response:**
- `200 OK` with `X-Sent-Bytes` header

### `GET /recv`

Poll for received messages. MCP and A2A messages are automatically routed to their respective servers; only unmatched messages appear here.

**Response:**
- `204 No Content` if queue is empty
- `200 OK` with raw binary body and `X-From-Peer-Id` header (sender's peer ID)

### `POST /mcp/{peer_id}/{service}`

Send an MCP request to a remote peer's service. The request body is a raw JSON-RPC payload. The node wraps it in an MCP envelope, sends it over Yggdrasil TCP, and returns the JSON-RPC response.

### `POST /a2a/{peer_id}`

Send an A2A request to a remote peer. The request body is a raw A2A JSON-RPC payload. The node wraps it in an A2A envelope, sends it over Yggdrasil TCP, and returns the JSON-RPC response.

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

## How It Works

1. **Yggdrasil Core** — Generates a keypair (if not provided in config), derives an IPv6 address (`200::/7`), and connects to peers
2. **gVisor Stack** — Provides a userspace TCP/IP stack bound to the Yggdrasil IPv6 address
3. **TCP Listener** — Listens on port 7000 (internal) for incoming messages from other nodes
4. **HTTP Bridge** — Exposes `/send`, `/recv`, `/topology`, `/mcp/`, and `/a2a/` endpoints on localhost

**Outbound (`/send`):** Sends a length-prefixed message to a remote peer. Fire-and-forget, no response is read back.

**Outbound (`/mcp/{peer_id}/{service}` and `/a2a/{peer_id}`):** Wraps the request in a transport envelope, dials the remote peer, sends the envelope, waits for a response (30s timeout), unwraps the response, and returns it to the caller.

**Inbound (TCP listener):** Accepts connections from the overlay and reads length-prefixed messages. The multiplexer checks each registered stream:
1. `"service"` field → MCP request → forwarded to the local MCP router
2. `"a2a": true` → A2A request → forwarded to the local A2A server
3. Unmatched → queued for `/recv`

The stream's response is sent back to the remote peer over the same TCP connection.

### Wire Format

All TCP messages use a length-prefixed envelope:

| Envelope | Discriminator | Forwarded to |
|----------|---------------|-------------|
| `{"service":"...","request":{...}}` | `service` field present | MCP router |
| `{"a2a":true,"request":{...}}` | `a2a` field is `true` | A2A server |
| anything else | — | `/recv` queue |

## Submodules

- **yggdrasil-go**: Official Yggdrasil implementation (https://github.com/yggdrasil-network/yggdrasil-go)
