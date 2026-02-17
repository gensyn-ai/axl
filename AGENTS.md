# AGENTS.md

## What This Project Is

This is a P2P network node. The Go binary (`node`) handles all network transport — peering, encryption, routing — and exposes a local HTTP API on `127.0.0.1:9002`. Your application talks to this API; it never touches the network directly.

The node is application-agnostic. It does not care what you send. It can carry raw bytes, JSON-RPC, tensors, or any other payload.

## Project Layout

```
cmd/node/              # Go entrypoint and config
api/                   # HTTP API handlers (send, recv, topology, mcp, a2a)
internal/
  tcp/listen/          # Inbound TCP listener, multiplexer, stream interface
  tcp/dial/            # Outbound peer dialing
  mcp/                 # MCP stream implementation
  a2a/                 # A2A stream implementation
integrations/          # Python services (MCP router, A2A server)
examples/python-client/  # Example Python clients
docs/                  # Detailed documentation
```

## How to Interact with the Node

Your application communicates with the node via HTTP on localhost. There are three core endpoints:

### Send data to a remote peer
```
POST http://127.0.0.1:9002/send
Header: X-Destination-Peer-Id: <64-char-hex-public-key>
Body: raw bytes (any format)
Response: 200 OK, X-Sent-Bytes header
```
Fire-and-forget. The node dials the remote peer, writes a length-prefixed message, and returns. No response from the remote peer is read back.

### Receive data from remote peers
```
GET http://127.0.0.1:9002/recv
Response: 204 (empty) or 200 with raw body + X-From-Peer-Id header
```
Poll-based. Messages arrive asynchronously; call `/recv` in a loop. Each call dequeues one message.

### Discover peers
```
GET http://127.0.0.1:9002/topology
Response: JSON with our_ipv6, our_public_key, peers[], tree[]
```
Use `our_public_key` to identify this node. Share it with other nodes so they can send to you.

### Protocol-specific endpoints
```
POST http://127.0.0.1:9002/mcp/{peer_id}/{service}   # JSON-RPC → remote MCP service
POST http://127.0.0.1:9002/a2a/{peer_id}              # JSON-RPC → remote A2A server
```
These are request/response (30s timeout). The node wraps your JSON-RPC body in a transport envelope, sends it, waits for the response, unwraps it, and returns it.

## Building an Application

### Minimal pattern: send/recv

The simplest integration is a script that sends and receives raw bytes. See `examples/python-client/client.py` for a working example. The pattern is:

1. Start the node: `./node -config node-config.json`
2. Get your public key: `GET /topology` → `our_public_key`
3. Share your public key with the peer you want to talk to
4. Send: `POST /send` with the peer's public key and your payload
5. Receive: poll `GET /recv` in a loop

You choose the serialization. The examples use `msgpack` for tensors; JSON, protobuf, or raw bytes all work.

### Adding a new protocol/stream

The node multiplexes inbound messages using a `Stream` interface (`internal/tcp/listen/stream.go`):

```go
type Stream interface {
    GetID() string
    IsAllowed(data []byte, metadata any) bool
    Forward(metadata any, fromPeerId string) ([]byte, error)
}
```

`IsAllowed` inspects the raw message bytes and returns `true` if this stream should handle it. `Forward` processes the message and returns a response. Unmatched messages go to the `/recv` queue.

Existing streams use JSON envelope fields as discriminators:
- MCP: `{"service": "...", "request": {...}}` → `service` field present
- A2A: `{"a2a": true, "request": {...}}` → `a2a` field is `true`

To add a new protocol, implement `Stream`, register it in the listener (`internal/tcp/listen/listener.go`), and add an HTTP endpoint in `api/`.

### Wire format

All TCP messages between nodes are length-prefixed: a 4-byte big-endian `uint32` length followed by that many bytes of payload. Max message size defaults to 16 MB (configurable via `max_message_size` in `node-config.json`). Helper functions are in `api/tcp_helpers.go`.

## Build and Test

### Go
```bash
go build -o node ./cmd/node/
go test ./...
```

### Python integrations
```bash
cd integrations
pip install -e ".[test]"
pytest
```

## Configuration

All configuration is in `node-config.json`. Key fields for application developers:

| Field | Default | What it controls |
|-------|---------|-----------------|
| `Peers` | `[]` | Yggdrasil peer URIs to connect to |
| `PrivateKeyPath` | _(none)_ | Persistent identity; omit for ephemeral |
| `api_port` | `9002` | HTTP API port your app connects to |
| `router_addr` | _(empty)_ | MCP Router host; empty = MCP disabled |
| `a2a_addr` | _(empty)_ | A2A Server host; empty = A2A disabled |
| `max_message_size` | `16777216` | Max payload size in bytes |

See `docs/configuration.md` for the complete reference.

## Key Constraints

- **No direct network access.** Your application never opens sockets to remote peers. All communication goes through the node's HTTP API.
- **Peer identity = ed25519 public key.** Peers are addressed by their 64-character hex-encoded public key, not by IP or hostname.
- **`/send` is fire-and-forget.** If you need request/response semantics, either use `/mcp/` or `/a2a/`, or build your own over `/send` + `/recv`.
- **`/recv` is a queue.** Messages are dequeued on read. If nobody polls `/recv`, unmatched messages accumulate in memory.
- **Message size limit.** Default 16 MB per message. Configurable, but very large messages will consume memory on both sides.
- **Both nodes must be running.** There is no store-and-forward. If the remote peer is offline, `/send` will fail with a dial error.
