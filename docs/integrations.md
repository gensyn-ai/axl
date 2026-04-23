# Integrations (Python)

The `integrations/` directory contains Python services that run alongside the Go node. The node handles network transport; these services handle application-level protocols.

```
integrations/
  mcp_routing/
    mcp_router.py        # MCP request router (:9003)
  a2a_serving/
    a2a_server.py        # A2A protocol server (:9004)
  pyproject.toml
```

## Install

```bash
cd integrations
pip install -e .
```

## MCP Router

The router is a lightweight HTTP gateway that sits between the Yggdrasil P2P bridge and your MCP servers. It allows a single node to host any number of independent services: the bridge forwards every incoming MCP request to the router, which dispatches it to the correct backend by name. Apart from the registration and deregistration calls described below, the router is completely transparent to your server — it never changes request or response payloads.

Start the router before launching any MCP server:

```bash
python -m mcp_routing.mcp_router --port 9003
```

| Endpoint | Description |
|----------|-------------|
| `POST /route` | Forward a request to a registered service (called by the bridge) |
| `POST /register` | Register a service (`{"service": "...", "endpoint": "..."}`) |
| `DELETE /register/{service}` | Remove a service |
| `GET /services` | List registered services |
| `GET /health` | Router health check |

### Writing Your Own MCP Server

To make your server reachable over the Yggdrasil network you must:

1. **Start the router first.** The router must already be running when your server starts, because registration happens at server startup via an HTTP call.

2. **Register on startup.** Call `POST /register` with your service name and the full URL of your server's MCP endpoint:

   ```http
   POST http://127.0.0.1:9003/register
   Content-Type: application/json

   {
     "service": "my-service",
     "endpoint": "http://127.0.0.1:7100/mcp"
   }
   ```

   Until this call succeeds, the router has no record of your server and any incoming requests for it will return a 404.

3. **Deregister on shutdown.** When your server exits it should call `DELETE /register/{service}` to remove itself from the router's table cleanly:

   ```http
   DELETE http://127.0.0.1:9003/register/my-service
   ```

   This prevents the router from routing requests to a dead endpoint.

**Minimal Python example** (using `aiohttp`):

```python
import asyncio
from aiohttp import ClientSession, ClientTimeout

ROUTER_URL = "http://127.0.0.1:9003"
SERVICE_NAME = "my-service"
SERVICE_ENDPOINT = "http://127.0.0.1:7100/mcp"
_timeout = ClientTimeout(total=5)

async def register():
    async with ClientSession(timeout=_timeout) as s:
        async with s.post(
            f"{ROUTER_URL}/register",
            json={"service": SERVICE_NAME, "endpoint": SERVICE_ENDPOINT},
        ) as resp:
            resp.raise_for_status()

async def deregister():
    async with ClientSession(timeout=_timeout) as s:
        await s.delete(f"{ROUTER_URL}/register/{SERVICE_NAME}")

# In your startup/shutdown flow:
# asyncio.run(register())   # before accepting traffic
# ...
# asyncio.run(deregister()) # in a finally block on exit
```

## A2A Server

Exposes registered MCP services as [A2A](https://github.com/google/A2A) skills. Auto-discovers services from the router and advertises them at `/.well-known/agent-card.json`.

```bash
python -m a2a_serving.a2a_server --port 9004 --router http://127.0.0.1:9003
```

The a2a server may be reached across the Gensyn network using the peerId of the node running the server.  A `Get` will return the `/.well-known/agent-card.json` file, and a `POST` will route the request to the appropriate MCP service.
```HTTP
GET /a2a/<peerId>
```

```HTTP
POST /a2a/<peerId>
```

## A2A Test Client

Located at `examples/python-client/a2a_client.py`. Routes requests through the local Gensyn node to a remote peer's A2A server:

```bash
python examples/python-client/a2a_client.py \
  --peer-id <64-char-hex-public-key> \
  --service weather --method tools/list
```

## Tests

```bash
cd integrations
pip install -e ".[test]"
pytest
```
