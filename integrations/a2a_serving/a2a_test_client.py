"""Test client for the A2A server, supporting both local and remote modes.

Local mode (default): sends directly to a local A2A server using the A2A SDK.
Remote mode: sends through the local yggdrasil node to a remote peer's A2A server.

Usage:
    # Local mode - hit A2A server directly
    python a2a_test_client.py --service weather --method tools/list
    python a2a_test_client.py --url http://localhost:9004 --service weather

    # Remote mode - route through yggdrasil network
    python a2a_test_client.py --remote --peer-id <REMOTE_PEER_ID> --service weather
    python a2a_test_client.py --remote --peer-id <REMOTE_PEER_ID> --node-url http://localhost:9002
"""

import argparse
import asyncio
import json
import logging
from uuid import uuid4

import httpx

from a2a.client import A2ACardResolver, A2AClient
from a2a.types import (
    MessageSendParams,
    SendMessageRequest,
)


logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)


def build_mcp_request(service: str, method: str) -> dict:
    """Build the inner MCP request payload."""
    return {
        "service": service,
        "request": {
            "jsonrpc": "2.0",
            "method": method,
            "id": 1,
            "params": {},
        },
    }


async def run_local(base_url: str, service: str, method: str):
    """Send a request directly to a local A2A server using the A2A SDK."""

    async with httpx.AsyncClient() as httpx_client:
        logger.info(f"Fetching agent card from {base_url}")
        resolver = A2ACardResolver(
            httpx_client=httpx_client,
            base_url=base_url,
        )

        agent_card = await resolver.get_agent_card()
        logger.info(f"Agent: {agent_card.name}")
        logger.info(f"Skills: {[s.id for s in agent_card.skills]}")

        client = A2AClient(httpx_client=httpx_client, agent_card=agent_card)

        mcp_request = build_mcp_request(service, method)

        message_payload = {
            "message": {
                "role": "user",
                "parts": [
                    {"kind": "text", "text": json.dumps(mcp_request)}
                ],
                "messageId": uuid4().hex,
            },
        }

        request = SendMessageRequest(
            id=str(uuid4()),
            params=MessageSendParams(**message_payload),
        )

        logger.info(f"Sending request to {service} service...")
        response = await client.send_message(request)

        print(json.dumps(response.model_dump(mode="json", exclude_none=True), indent=2))


async def run_remote(node_url: str, peer_id: str, service: str, method: str):
    """Send an A2A request to a remote peer via the local yggdrasil node."""

    mcp_request = build_mcp_request(service, method)

    # Build the A2A JSON-RPC message/send request
    a2a_request = {
        "jsonrpc": "2.0",
        "method": "message/send",
        "id": str(uuid4()),
        "params": {
            "message": {
                "role": "user",
                "parts": [
                    {"kind": "text", "text": json.dumps(mcp_request)}
                ],
                "messageId": uuid4().hex,
            },
        },
    }

    # POST to the local node's /a2a/{peer_id} endpoint.
    # The node wraps this in {"a2a": true, "request": ...}, sends it
    # over Yggdrasil TCP to the remote peer, and returns the unwrapped response.
    url = f"{node_url}/a2a/{peer_id}"
    logger.info(f"Sending A2A request to remote peer {peer_id[:16]}... via {node_url}")
    logger.info(f"Service: {service}, Method: {method}")

    async with httpx.AsyncClient(timeout=60.0) as client:
        response = await client.post(url, json=a2a_request)

    if response.status_code != 200:
        logger.error(f"STATUS: {response.status_code}")
        logger.error(response.text)
        return

    try:
        data = response.json()
        print(json.dumps(data, indent=2))
    except json.JSONDecodeError:
        print(response.text)


def run():
    """Entry point."""
    parser = argparse.ArgumentParser(
        description="Test A2A client — supports local and remote (yggdrasil) modes"
    )
    parser.add_argument(
        "--remote",
        action="store_true",
        help="Route request through yggdrasil network to a remote peer",
    )
    parser.add_argument(
        "--peer-id",
        type=str,
        default=None,
        help="Remote peer's public key (64-char hex). Required with --remote",
    )
    parser.add_argument(
        "--node-url",
        type=str,
        default="http://localhost:9002",
        help="Local yggdrasil node API URL (default: http://localhost:9002). Used with --remote",
    )
    parser.add_argument(
        "--url",
        type=str,
        default="http://localhost:9004",
        help="A2A server URL for local mode (default: http://localhost:9004)",
    )
    parser.add_argument(
        "--service",
        type=str,
        default="weather",
        help="MCP service to call (default: weather)",
    )
    parser.add_argument(
        "--method",
        type=str,
        default="tools/list",
        help="MCP method to call (default: tools/list)",
    )
    args = parser.parse_args()

    if args.remote:
        if not args.peer_id:
            parser.error("--peer-id is required when using --remote")
        asyncio.run(run_remote(args.node_url, args.peer_id, args.service, args.method))
    else:
        asyncio.run(run_local(args.url, args.service, args.method))


if __name__ == "__main__":
    run()
