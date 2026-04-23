# HTTP API

The node exposes a local HTTP server on `127.0.0.1:9002` (configurable via `api_port` and `bridge_addr` in `node-config.json`).

## Endpoints

### `GET /topology`

Returns this node's address, public key, and peer/tree state.

```json
{
  "our_ipv6": "200:abcd:...",
  "our_public_key": "abcd1234...",
  "peers": [...],
  "tree": [...]
}
```

### `POST /send`

Fire-and-forget message to another node.

| Header | Value |
|--------|-------|
| `X-Destination-Peer-Id` | Hex-encoded ed25519 public key (64 chars) |

**Body:** raw binary data.
**Response:** `200 OK` with `X-Sent-Bytes` header.

### `GET /recv`

Poll for inbound messages. Messages matching MCP or A2A envelopes are routed automatically and do not appear here.

- `204 No Content` — queue is empty
- `200 OK` — raw binary body with `X-From-Peer-Id` header

### `POST /mcp/{peer_id}/{service}`

Send a JSON-RPC request to a remote peer's MCP service. The node wraps it in a transport envelope, sends it over Yggdrasil, and returns the JSON-RPC response.

### `POST /a2a/{peer_id}`

Send a JSON-RPC request to a remote peer's A2A server. Same transport as `/mcp/` but using the A2A envelope.

## Examples

In all examples, replace `{peer_id}` with the remote peer's hex-encoded public key (64 characters).

**List tools on a remote peer's MCP service:**
```bash
curl -X POST http://127.0.0.1:9002/mcp/{peer_id}/weather \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"tools/list","id":1,"params":{}}'
```

**Call a tool on a remote peer's MCP service:**
```bash
curl -X POST http://127.0.0.1:9002/mcp/{peer_id}/weather \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"tools/call","id":1,"params":{"name":"get_weather","arguments":{"city":"Dublin"}}}'
```

**List tools via A2A:**
```bash
curl -X POST http://127.0.0.1:9002/a2a/{peer_id} \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "method": "SendMessage",
    "id": 1,
    "params": {
      "message": {
        "role": "ROLE_USER",
        "parts": [{"text": "{\"service\":\"weather\",\"request\":{\"jsonrpc\":\"2.0\",\"method\":\"tools/list\",\"id\":1,\"params\":{}}}"}],
        "messageId": "test123"
      }
    }
  }'
```

**Call a tool via A2A:**
```bash
curl -X POST http://127.0.0.1:9002/a2a/{peer_id} \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "method": "SendMessage",
    "id": 1,
    "params": {
      "message": {
        "role": "ROLE_USER",
        "parts": [{"text": "{\"service\":\"weather\",\"request\":{\"jsonrpc\":\"2.0\",\"method\":\"tools/call\",\"id\":1,\"params\":{\"name\":\"get_weather\",\"arguments\":{\"city\":\"Dublin\"}}}}"}],
        "messageId": "test123"
      }
    }
  }'
```

The `messageId` is a client-assigned correlation ID. The text part must be a JSON-stringified MCP request matching the format the A2A server expects.
