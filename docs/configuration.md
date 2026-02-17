# Configuration

## Build and Run

```bash
go build -o node ./cmd/node/
./node -config node-config.json
```

Optionally generate a persistent identity (otherwise one is created in memory at startup):
```bash
openssl genpkey -algorithm ed25519 -out private.pem
```

Run the node tests:
```bash
go test ./...
```

## CLI Flags

| Flag | Description | Default |
|------|-------------|---------|
| `-config` | Path to config file | `node-config.json` |
| `-listen` | Listen address for incoming peers | _(none)_ |

`-listen` should be used if you are hosting a public node and are willing to accept routing requests from other nodes.  This will require a user to manually expose a port and IP address to the public internet. Otherwise, it is not recommended to use `-listen`.

Example:
```bash
./node -listen tls://0.0.0.0:9001
```

## node-config.json

The config file is shared between Yggdrasil's own settings and the node's API/transport settings. All fields are optional — defaults are applied for anything omitted.

### Yggdrasil settings

| Field | Description | Example |
|-------|-------------|---------|
| `PrivateKeyPath` | Path to ed25519 PEM key | `"private.pem"` |
| `Peers` | List of peer URIs to connect to | `["tls://1.2.3.4:9001"]` |
| `Listen` | Addresses to listen for incoming peers | `["tls://0.0.0.0:9001"]` |

### Node settings

| Field | Default | Description |
|-------|---------|-------------|
| `api_port` | `9002` | HTTP API listen port |
| `bridge_addr` | `127.0.0.1` | HTTP API bind address |
| `tcp_port` | `7000` | Internal TCP listener port (gVisor) |
| `router_addr` | _(empty)_ | MCP Router host (e.g. `http://127.0.0.1`). Empty = disabled. |
| `router_port` | `9003` | MCP Router port |
| `a2a_addr` | _(empty)_ | A2A Server host (e.g. `http://127.0.0.1`). Empty = disabled. |
| `a2a_port` | `9004` | A2A Server port |
| `max_message_size` | `16777216` | Max TCP message size in bytes (16 MB) |
| `max_concurrent_conns` | `128` | Max concurrent inbound TCP connections |
| `conn_read_timeout_secs` | `60` | Read timeout per connection (seconds) |
| `conn_idle_timeout_secs` | `300` | Idle timeout per connection (seconds) |

### Example

```json
{
  "PrivateKeyPath": "private.pem",
  "Peers": ["tls://34.46.48.224:9001"],
  "router_addr": "http://127.0.0.1",
  "router_port": 9003,
  "a2a_addr": "http://127.0.0.1",
  "a2a_port": 9004
}
```

Setting `router_addr` and/or `a2a_addr` enables the corresponding stream. If left empty, inbound messages of that type are ignored.
