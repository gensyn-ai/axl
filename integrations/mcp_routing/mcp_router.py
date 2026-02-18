"""MCP Router Service.

Routes MCP requests from the Yggdrasil P2P bridge to registered MCP servers.
Allows dynamic registration/deregistration of services without restarting the bridge.
"""

import argparse
import asyncio
import logging
from datetime import datetime, timezone
from aiohttp import web, ClientSession, ClientTimeout

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

# Default router port
ROUTER_PORT = 9003

# Service registry: service_name -> service_info
services: dict[str, dict] = {}


async def handle_route(request: web.Request) -> web.Response:
    """Route an MCP request to the appropriate service.

    Called by client.go for all incoming MCP traffic.

    Request body:
    {
        "service": "weather",
        "request": {"jsonrpc": "2.0", ...},
        "from_peer_id": "abc123..."
    }

    Response:
    {
        "response": {"jsonrpc": "2.0", ...},
        "error": null
    }
    """
    try:
        body = await request.json()
    except Exception as e:
        return web.json_response(
            {"response": None, "error": f"Invalid JSON: {e}"},
            status=400,
        )

    service_name = body.get("service", "")
    mcp_request = body.get("request")
    from_peer_id = body.get("from_peer_id", "unknown")

    if not service_name:
        return web.json_response(
            {"response": None, "error": "Missing 'service' field"},
            status=400,
        )

    if service_name not in services:
        logger.warning(f"Service not found: {service_name}")
        return web.json_response(
            {"response": None, "error": f"Service not found: {service_name}"},
            status=404,
        )

    service = services[service_name]
    endpoint = service["endpoint"]

    logger.info(f"Routing request to {service_name} from peer {from_peer_id[:16]}...")

    # Forward to the MCP server
    try:
        async with ClientSession(timeout=ClientTimeout(total=30)) as session:
            async with session.post(
                endpoint,
                json=mcp_request,
                headers={
                    "Content-Type": "application/json",
                    "Accept": "application/json, text/event-stream", # required for MCP servers
                    "X-From-Peer-Id": from_peer_id,
                    "X-Service": service_name,
                },
            ) as resp:
                # Handle 204 No Content (notifications)
                if resp.status == 204:
                    return web.json_response({"response": None, "error": None})

                if resp.status != 200:
                    error_text = await resp.text()
                    logger.error(f"Service {service_name} returned {resp.status}: {error_text}")
                    services[service_name]["healthy"] = False
                    return web.json_response(
                        {"response": None, "error": f"Service error: {resp.status}"},
                        status=502,
                    )

                response_data = await resp.json()
                services[service_name]["healthy"] = True
                return web.json_response({"response": response_data, "error": None})

    except asyncio.TimeoutError:
        logger.error(f"Timeout forwarding to {service_name}")
        services[service_name]["healthy"] = False
        return web.json_response(
            {"response": None, "error": "Service timeout"},
            status=504,
        )
    except Exception as e:
        logger.error(f"Error forwarding to {service_name}: {e}")
        services[service_name]["healthy"] = False
        return web.json_response(
            {"response": None, "error": f"Forward error: {e}"},
            status=502,
        )


async def handle_register(request: web.Request) -> web.Response:
    """Register an MCP service.

    Request body:
    {
        "service": "weather",
        "endpoint": "http://127.0.0.1:7100/mcp"
    }
    """
    try:
        body = await request.json()
    except Exception as e:
        return web.json_response({"error": f"Invalid JSON: {e}"}, status=400)

    service_name = body.get("service", "")
    endpoint = body.get("endpoint", "")

    if not service_name or not endpoint:
        return web.json_response(
            {"error": "Both 'service' and 'endpoint' are required"},
            status=400,
        )

    services[service_name] = {
        "endpoint": endpoint,
        "registered_at": datetime.now(timezone.utc).isoformat(),
        "healthy": True,
    }

    logger.info(f"Registered service: {service_name} -> {endpoint}")
    return web.json_response({"status": "registered", "service": service_name})


async def handle_deregister(request: web.Request) -> web.Response:
    """Deregister an MCP service."""
    service_name = request.match_info.get("service", "")

    if not service_name:
        return web.json_response({"error": "Service name required"}, status=400)

    if service_name not in services:
        return web.json_response({"error": f"Service not found: {service_name}"}, status=404)

    del services[service_name]
    logger.info(f"Deregistered service: {service_name}")
    return web.json_response({"status": "deregistered", "service": service_name})


async def handle_services(request: web.Request) -> web.Response:
    """List all registered services."""
    return web.json_response(services)


async def handle_health(request: web.Request) -> web.Response:
    """Router health check."""
    return web.json_response({
        "status": "ok",
        "service_count": len(services),
    })


async def run_router(port: int):
    """Run the MCP router HTTP server."""
    app = web.Application()
    app.router.add_post("/route", handle_route)
    app.router.add_post("/register", handle_register)
    app.router.add_delete("/register/{service}", handle_deregister)
    app.router.add_get("/services", handle_services)
    app.router.add_get("/health", handle_health)

    runner = web.AppRunner(app)
    await runner.setup()
    site = web.TCPSite(runner, "127.0.0.1", port)
    await site.start()

    logger.info(f"MCP Router listening on http://127.0.0.1:{port}")
    logger.info("Endpoints:")
    logger.info(f"  POST   /route              - Route MCP request (called by bridge)")
    logger.info(f"  POST   /register           - Register a service")
    logger.info(f"  DELETE /register/{{service}} - Deregister a service")
    logger.info(f"  GET    /services           - List registered services")
    logger.info(f"  GET    /health             - Health check")

    # Keep running
    while True:
        await asyncio.sleep(3600)


def main():
    """Entry point."""
    parser = argparse.ArgumentParser(description="MCP Router Service")
    parser.add_argument(
        "--port",
        type=int,
        default=ROUTER_PORT,
        help=f"Port to listen on (default: {ROUTER_PORT})",
    )
    args = parser.parse_args()

    asyncio.run(run_router(args.port))


if __name__ == "__main__":
    main()
