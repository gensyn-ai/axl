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

Routes MCP JSON-RPC requests to registered service backends. Services register themselves at startup.

```bash
python -m mcp_routing.mcp_router --port 9003
```

| Endpoint | Description |
|----------|-------------|
| `POST /route` | Forward a request to a registered service |
| `POST /register` | Register a service (`{"name": "...", "endpoint": "..."}`) |
| `POST /deregister` | Remove a service |
| `GET /services` | List registered services |

## A2A Server

Exposes registered MCP services as [A2A](https://github.com/google/A2A) skills. Auto-discovers services from the router and advertises them at `/.well-known/agent.json`.

```bash
python -m a2a_serving.a2a_server --port 9004 --router http://127.0.0.1:9003
```

## A2A Test Client

Located at `examples/python-client/a2a_client.py`. Supports two modes:

**Local** — talk directly to a local A2A server:
```bash
python examples/python-client/a2a_client.py --service weather --method tools/list
```

**Remote** — route through the Yggdrasil network to a remote peer:
```bash
python examples/python-client/a2a_client.py \
  --remote --peer-id <64-char-hex-public-key> \
  --service weather --method tools/list
```

## Tests

```bash
cd integrations
pip install -e ".[test]"
pytest
```
