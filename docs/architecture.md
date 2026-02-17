# Architecture

## Components

```
                        localhost
                 ┌──────────────────┐
                 │                  │
    Your App ◄───┤ HTTP API (:9002) │
                 │                  │
                 │   Multiplexer   ─┼──► MCP Router (:9003)
                 │        │         │
                 │   gVisor TCP     ├──► A2A Server (:9004)
                 │        │         │
                 │   Yggdrasil Core │
                 │        │         │
                 └────────┼─────────┘
                          │ TLS/TCP
                          ▼
                     Gensyn Peers
```

The Go binary (`node`) contains four layers:

| Layer | Role |
|-------|------|
| **HTTP API** | Local REST server on `127.0.0.1:9002`. Exposes `/send`, `/recv`, `/topology`, `/mcp/`, `/a2a/`. |
| **Multiplexer** | Routes inbound TCP messages to the correct handler based on envelope fields. |
| **gVisor TCP** | Userspace TCP/IP stack — no TUN device or root privileges needed. Listens on port 7000 for inbound connections. |
| **Yggdrasil Core** | Manages keypair, derives an IPv6 address (`200::/7`), and peers over TLS/TCP. |

The **MCP Router** and **A2A Server** are separate processes (Python). The node forwards matching inbound messages to them via HTTP.

## Data Flow

### Outbound

**`/send`** — Fire-and-forget. Dials the remote peer, writes a length-prefixed message, closes the connection. No response.

**`/mcp/{peer_id}/{service}` and `/a2a/{peer_id}`** — Request/response. Wraps the JSON-RPC body in a transport envelope, dials the remote peer, sends the envelope, waits for a response (30s timeout), unwraps and returns it.

### Inbound

When a remote peer connects on port 7000, the node reads a length-prefixed message and the multiplexer checks it against registered streams:

1. `"service"` field present → forward to MCP Router via HTTP
2. `"a2a": true` → forward to A2A Server via HTTP
3. No match → queue for `/recv`

The handler's response is sent back over the same TCP connection.

## Wire Format

All TCP messages are length-prefixed: a 4-byte big-endian `uint32` length followed by that many bytes of payload. Maximum message size defaults to 16 MB.

Envelope structure determines routing:

| Envelope | Routed to |
|----------|-----------|
| `{"service":"...","request":{...}}` | MCP Router |
| `{"a2a":true,"request":{...}}` | A2A Server |
| anything else | `/recv` queue |

## Yggdrasil Core

- [yggdrasil-go](https://github.com/yggdrasil-network/yggdrasil-go) — official Yggdrasil implementation
