"""Test client for sending A2A requests through the local Gensyn node to a remote peer.

Usage:
    python a2a_client.py --peer-id <REMOTE_PEER_ID> --service weather
    python a2a_client.py --peer-id <REMOTE_PEER_ID> --node-url http://localhost:9002
"""

import argparse
import asyncio
import json
import logging
from uuid import uuid4

import httpx


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


async def fetch_agent_card(node_url: str, peer_id: str):
    """Fetch the agent card from a remote peer via the local Gensyn node."""
    url = f"{node_url}/a2a/{peer_id}"
    logger.info(f"Fetching agent card from remote peer {peer_id[:16]}... via {node_url}")

    async with httpx.AsyncClient(timeout=60.0) as client:
        response = await client.get(url)

    if response.status_code != 200:
        logger.error(f"STATUS: {response.status_code}")
        logger.error(response.text)
        return

    try:
        data = response.json()
        print(json.dumps(data, indent=2))
    except json.JSONDecodeError:
        print(response.text)


async def send_request(node_url: str, peer_id: str, service: str, method: str):
    """Send an A2A request to a remote peer via the local Gensyn node."""
    await fetch_agent_card(node_url, peer_id)

    mcp_request = build_mcp_request(service, method)

    # Build the A2A JSON-RPC SendMessage request (v1.0)
    a2a_request = {
        "jsonrpc": "2.0",
        "method": "SendMessage",
        "id": str(uuid4()),
        "params": {
            "message": {
                "role": "ROLE_USER",
                "parts": [
                    {"text": json.dumps(mcp_request)}
                ],
                "messageId": uuid4().hex,
            },
        },
    }

    # POST to the local node's /a2a/{peer_id} endpoint.
    # The node wraps this in {"a2a": true, "request": ...}, sends it
    # over the Gensyn Network to the remote peer, and returns the unwrapped response.
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
        description="Test A2A client — routes requests through the local Gensyn node to a remote peer"
    )
    parser.add_argument(
        "--peer-id",
        type=str,
        required=True,
        help="Remote peer's public key (64-char hex)",
    )
    parser.add_argument(
        "--node-url",
        type=str,
        default="http://localhost:9002",
        help="Local Gensyn node API URL (default: http://localhost:9002)",
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

    asyncio.run(send_request(args.node_url, args.peer_id, args.service, args.method))


if __name__ == "__main__":
    run()
