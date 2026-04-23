# Examples

## Remote MCP Server

This example connects two machines over Yggdrasil so one can call MCP tools hosted on the other. A2A is **not** required — the node's `/mcp/` endpoint talks directly to a remote peer's MCP router.

### Remote machine (serves tools)

```bash
# Start the node
./node -config node-config.json

# Start the MCP router
python -m mcp_routing.mcp_router

# Start your MCP service(s) and register them with the router
```

### Local machine (sends requests)

```bash
# Start the node (must share at least one common peer with the remote)
./node -config node-config.json

# Get the remote node's public key
curl http://localhost:9002/topology | jq .our_public_key

# List tools on the remote peer's "weather" service
curl -X POST http://127.0.0.1:9002/mcp/<remote-public-key>/weather \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"tools/list","id":1,"params":{}}'
```

Both nodes must be able to reach at least one common Yggdrasil peer (configured in `node-config.json` under `Peers`). They do not need direct connectivity to each other.

## Adding A2A

A2A is an optional layer complimentary with MCP. It exposes your registered MCP services as [A2A skills](https://github.com/google/A2A), allowing any A2A-compatible agent to discover and call them. If you only need remote MCP access, you can skip this.

### Remote machine (add A2A to an existing MCP setup)

```bash
# After starting the node, MCP router (see above), and mcp server, add the A2A server
python -m a2a_serving.a2a_server
```
The a2a server will auto discover the mcp server and register the mcp services as A2A skills.

### Local machine

```bash
# Using the test client
python examples/python-client/a2a_client.py \
  --peer-id <remote-public-key> \
  --service weather --method tools/list

# Or via curl
curl -X POST http://127.0.0.1:9002/a2a/<remote-public-key> \
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
