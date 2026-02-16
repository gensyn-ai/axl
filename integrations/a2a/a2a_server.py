"""A2A Server for deMCP.

This server sits in front of the MCP router and exposes registered MCP services
as A2A skills. It handles A2A protocol communication and forwards requests to
the router, returning results as A2A task artifacts.

Architecture:
    A2A Client -> A2A Server (:9004) -> Router (:9003) -> MCP Servers
"""

import argparse
import asyncio
import json
import logging
from typing import Any

import httpx
import uvicorn

from a2a.server.agent_execution import AgentExecutor, RequestContext
from a2a.server.apps import A2AStarletteApplication
from a2a.server.events import EventQueue
from a2a.server.request_handlers import DefaultRequestHandler
from a2a.server.tasks import InMemoryTaskStore
from a2a.types import (
    AgentCapabilities,
    AgentCard,
    AgentSkill,
    TaskArtifactUpdateEvent,
    TaskState,
    TaskStatus,
    TaskStatusUpdateEvent,
)
from a2a.utils import new_agent_text_message, new_task, new_text_artifact


logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

# Default ports
A2A_PORT = 9004
ROUTER_URL = "http://127.0.0.1:9003"


class MCPRouterAgentExecutor(AgentExecutor):
    """AgentExecutor that forwards requests to the MCP router.

    This executor:
    1. Receives A2A task requests
    2. Extracts the MCP service and request from the message
    3. Forwards to the router's /route endpoint
    4. Returns the MCP response as a task artifact
    """

    def __init__(self, router_url: str):
        self.router_url = router_url
        self.client = httpx.AsyncClient(timeout=30.0)

    async def execute(
        self,
        context: RequestContext,
        event_queue: EventQueue,
    ) -> None:
        """Execute the A2A task by forwarding to the MCP router."""
        logger.info("Executing A2A task")

        # Get or create task
        task = context.current_task
        if not task:
            if not context.message:
                raise Exception("No message provided")
            task = new_task(context.message)
            await event_queue.enqueue_event(task)

        # Update status to working
        await event_queue.enqueue_event(
            TaskStatusUpdateEvent(
                status=TaskStatus(
                    state=TaskState.working,
                    message=new_agent_text_message(
                        "Processing MCP request...",
                        task.context_id,
                        task.id,
                    ),
                ),
                final=False,
                context_id=task.context_id,
                task_id=task.id,
            )
        )

        try:
            # Extract user input and parse as MCP request
            user_input = context.get_user_input()
            mcp_request = self._parse_mcp_request(user_input)

            if not mcp_request:
                raise ValueError("Could not parse MCP request from message")

            service_name = mcp_request.get("service", "")
            request_body = mcp_request.get("request", {})

            if not service_name:
                raise ValueError("No service specified in request")

            logger.info(f"Forwarding to router: service={service_name}")

            # Forward to router
            response = await self.client.post(
                f"{self.router_url}/route",
                json={
                    "service": service_name,
                    "request": request_body,
                    "from_peer_id": "a2a-client",
                },
            )

            if response.status_code != 200:
                raise Exception(f"Router returned {response.status_code}: {response.text}")

            result = response.json()

            # Check for router error
            if result.get("error"):
                raise Exception(f"Router error: {result['error']}")

            # Format response
            mcp_response = result.get("response", {})
            response_text = json.dumps(mcp_response, indent=2)

            # Return as completed task with artifact
            await event_queue.enqueue_event(
                TaskArtifactUpdateEvent(
                    append=False,
                    context_id=task.context_id,
                    task_id=task.id,
                    last_chunk=True,
                    artifact=new_text_artifact(
                        name="mcp_response",
                        description=f"Response from {service_name} MCP service",
                        text=response_text,
                    ),
                )
            )

            await event_queue.enqueue_event(
                TaskStatusUpdateEvent(
                    status=TaskStatus(state=TaskState.completed),
                    final=True,
                    context_id=task.context_id,
                    task_id=task.id,
                )
            )

            logger.info(f"Task completed successfully for service={service_name}")

        except Exception as e:
            logger.error(f"Task execution failed: {e}")

            # Return error as failed task
            await event_queue.enqueue_event(
                TaskStatusUpdateEvent(
                    status=TaskStatus(
                        state=TaskState.failed,
                        message=new_agent_text_message(
                            f"Error: {str(e)}",
                            task.context_id,
                            task.id,
                        ),
                    ),
                    final=True,
                    context_id=task.context_id,
                    task_id=task.id,
                )
            )

    def _parse_mcp_request(self, user_input: str) -> dict[str, Any] | None:
        """Parse the user input as an MCP request.

        Expected format:
        {
            "service": "weather",
            "request": {"jsonrpc": "2.0", "method": "tools/call", ...}
        }

        Or just a JSON-RPC request if service is in context/skill.
        """
        try:
            data = json.loads(user_input)

            # If it has service and request, use directly
            if "service" in data and "request" in data:
                return data

            # If it's a JSON-RPC request, try to infer service
            if "jsonrpc" in data:
                # Service would need to come from skill context
                # For now, return None and handle in execute()
                return {"service": "", "request": data}

            return data
        except json.JSONDecodeError:
            logger.warning(f"Could not parse input as JSON: {user_input[:100]}...")
            return None

    async def cancel(
        self, context: RequestContext, event_queue: EventQueue
    ) -> None:
        """Cancel is not supported for MCP requests."""
        raise Exception("Cancel not supported for MCP requests")


async def discover_skills_from_router(router_url: str) -> list[AgentSkill]:
    """Auto-discover skills from the router's /services endpoint.

    Each registered MCP service becomes an A2A skill.
    """
    try:
        async with httpx.AsyncClient() as client:
            response = await client.get(f"{router_url}/services")
            if response.status_code != 200:
                logger.warning(f"Could not fetch services from router: {response.status_code}")
                return []

            services = response.json()
            skills = []

            for name, info in services.items():
                skill = AgentSkill(
                    id=name,
                    name=f"{name.title()} Service",
                    description=f"MCP service: {name}. Forward JSON-RPC requests to this service.",
                    tags=[name, "mcp"],
                    examples=[
                        f'{{"service": "{name}", "request": {{"jsonrpc": "2.0", "method": "tools/list", "id": 1}}}}'
                    ],
                )
                skills.append(skill)
                logger.info(f"Discovered skill: {name}")

            return skills

    except Exception as e:
        logger.warning(f"Could not discover skills from router: {e}")
        return []


async def create_agent_card(
    host: str,
    port: int,
    router_url: str,
    name: str = "deMCP A2A Agent",
) -> AgentCard:
    """Create the agent card with auto-discovered skills."""
    skills = await discover_skills_from_router(router_url)

    # Add a default skill if no services are registered
    if not skills:
        skills = [
            AgentSkill(
                id="mcp_proxy",
                name="MCP Proxy",
                description="Forward MCP JSON-RPC requests to registered services",
                tags=["mcp", "proxy"],
                examples=[
                    '{"service": "weather", "request": {"jsonrpc": "2.0", "method": "tools/list", "id": 1}}'
                ],
            )
        ]

    return AgentCard(
        name=name,
        description="A2A agent that proxies requests to MCP services via the deMCP router",
        url=f"http://{host}:{port}/",
        version="1.0.0",
        default_input_modes=["text", "application/json"],
        default_output_modes=["text", "application/json"],
        capabilities=AgentCapabilities(
            streaming=True,
            push_notifications=False,
        ),
        skills=skills,
    )


async def run_server(host: str, port: int, router_url: str):
    """Run the A2A server."""
    logger.info(f"Creating agent card with skills from {router_url}")
    agent_card = await create_agent_card(host, port, router_url)

    logger.info(f"Agent card created with {len(agent_card.skills)} skills")
    for skill in agent_card.skills:
        logger.info(f"  - {skill.id}: {skill.name}")

    # Create request handler
    request_handler = DefaultRequestHandler(
        agent_executor=MCPRouterAgentExecutor(router_url),
        task_store=InMemoryTaskStore(),
    )

    # Create A2A application
    server = A2AStarletteApplication(
        agent_card=agent_card,
        http_handler=request_handler,
    )

    logger.info(f"Starting A2A server on http://{host}:{port}")
    logger.info(f"Agent card available at http://{host}:{port}/.well-known/agent.json")
    logger.info(f"Forwarding MCP requests to router at {router_url}")

    # Run with uvicorn
    config = uvicorn.Config(
        server.build(),
        host=host,
        port=port,
        log_level="info",
    )
    server_instance = uvicorn.Server(config)
    await server_instance.serve()


def main():
    """Entry point."""
    parser = argparse.ArgumentParser(description="deMCP A2A Server")
    parser.add_argument(
        "--host",
        type=str,
        default="127.0.0.1",
        help="Host to listen on (default: 127.0.0.1)",
    )
    parser.add_argument(
        "--port",
        type=int,
        default=A2A_PORT,
        help=f"Port to listen on (default: {A2A_PORT})",
    )
    parser.add_argument(
        "--router",
        type=str,
        default=ROUTER_URL,
        help=f"MCP router URL (default: {ROUTER_URL})",
    )
    args = parser.parse_args()

    asyncio.run(run_server(args.host, args.port, args.router))


if __name__ == "__main__":
    main()
