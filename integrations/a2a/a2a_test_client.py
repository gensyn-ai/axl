"""Simple test client for the A2A server.

Usage:
    python -m demcp.a2a_test_client [--url URL] [--service SERVICE]
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


async def main(base_url: str, service: str, method: str = "tools/list"):
    """Test the A2A server by sending a request."""

    async with httpx.AsyncClient() as httpx_client:
        # Fetch agent card
        logger.info(f"Fetching agent card from {base_url}")
        resolver = A2ACardResolver(
            httpx_client=httpx_client,
            base_url=base_url,
        )

        agent_card = await resolver.get_agent_card()
        logger.info(f"Agent: {agent_card.name}")
        logger.info(f"Skills: {[s.id for s in agent_card.skills]}")

        # Create client
        client = A2AClient(httpx_client=httpx_client, agent_card=agent_card)

        # Build MCP request
        mcp_request = {
            "service": service,
            "request": {
                "jsonrpc": "2.0",
                "method": method,
                "id": 1,
                "params": {},
            },
        }

        # Build A2A message
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

        print("\n" + "=" * 60)
        print("RESPONSE")
        print("=" * 60)
        print(json.dumps(response.model_dump(mode="json", exclude_none=True), indent=2))


def run():
    """Entry point."""
    parser = argparse.ArgumentParser(description="Test A2A client for deMCP")
    parser.add_argument(
        "--url",
        type=str,
        default="http://localhost:9004",
        help="A2A server URL (default: http://localhost:9004)",
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

    asyncio.run(main(args.url, args.service, args.method))


if __name__ == "__main__":
    run()
