"""Unit tests for the A2A Server."""

import json
from unittest.mock import AsyncMock, MagicMock, patch

import httpx
import pytest

from a2a_serving.a2a_server import (
    MCPRouterAgentExecutor,
    create_agent_card,
    discover_skills_from_router,
)


# ── MCPRouterAgentExecutor._parse_mcp_request Tests ────────────────


class TestParseMcpRequest:
    """Tests for parsing user input into MCP requests."""

    def setup_method(self):
        self.executor = MCPRouterAgentExecutor(router_url="http://127.0.0.1:9003")

    def test_parse_full_request(self):
        """Parse a complete request with service and request fields."""
        user_input = json.dumps({
            "service": "weather",
            "request": {"jsonrpc": "2.0", "method": "tools/list", "id": 1},
        })
        result = self.executor._parse_mcp_request(user_input)
        assert result["service"] == "weather"
        assert result["request"]["method"] == "tools/list"

    def test_parse_bare_jsonrpc(self):
        """Parse a bare JSON-RPC request (no service wrapper)."""
        user_input = json.dumps({
            "jsonrpc": "2.0",
            "method": "tools/call",
            "id": 1,
            "params": {"name": "get_weather"},
        })
        result = self.executor._parse_mcp_request(user_input)
        assert result["service"] == ""
        assert result["request"]["method"] == "tools/call"

    def test_parse_invalid_json(self):
        """Return None for non-JSON input."""
        result = self.executor._parse_mcp_request("not json at all")
        assert result is None

    def test_parse_empty_string(self):
        """Return None for empty string."""
        result = self.executor._parse_mcp_request("")
        assert result is None

    def test_parse_generic_dict(self):
        """Return the dict as-is if it has neither service+request nor jsonrpc."""
        user_input = json.dumps({"foo": "bar"})
        result = self.executor._parse_mcp_request(user_input)
        assert result == {"foo": "bar"}


# ── MCPRouterAgentExecutor.execute Tests ────────────────────────────


class TestExecute:
    """Tests for the main execute method."""

    def setup_method(self):
        self.executor = MCPRouterAgentExecutor(router_url="http://127.0.0.1:9003")

    @pytest.mark.asyncio
    async def test_execute_success(self):
        """Test successful execution forwarding to router."""
        mock_event_queue = AsyncMock()

        mock_context = MagicMock()
        mock_task = MagicMock()
        mock_task.id = "task-1"
        mock_task.context_id = "ctx-1"
        mock_context.current_task = mock_task
        mock_context.get_user_input.return_value = json.dumps({
            "service": "weather",
            "request": {"jsonrpc": "2.0", "method": "tools/list", "id": 1},
        })

        mock_response = MagicMock()
        mock_response.status_code = 200
        mock_response.json.return_value = {
            "response": {"jsonrpc": "2.0", "result": {"tools": []}, "id": 1},
            "error": None,
        }

        self.executor.client = AsyncMock()
        self.executor.client.post = AsyncMock(return_value=mock_response)

        await self.executor.execute(mock_context, mock_event_queue)

        # Should have enqueued: working status, artifact, completed status
        assert mock_event_queue.enqueue_event.call_count == 3

        # Verify the router was called correctly
        self.executor.client.post.assert_called_once()
        call_args = self.executor.client.post.call_args
        assert "/route" in call_args[0][0]
        assert call_args[1]["json"]["service"] == "weather"

    @pytest.mark.asyncio
    async def test_execute_router_error(self):
        """Test execution when router returns an error."""
        mock_event_queue = AsyncMock()

        mock_context = MagicMock()
        mock_task = MagicMock()
        mock_task.id = "task-1"
        mock_task.context_id = "ctx-1"
        mock_context.current_task = mock_task
        mock_context.get_user_input.return_value = json.dumps({
            "service": "weather",
            "request": {"jsonrpc": "2.0", "method": "tools/list", "id": 1},
        })

        mock_response = MagicMock()
        mock_response.status_code = 404
        mock_response.text = "Service not found"

        self.executor.client = AsyncMock()
        self.executor.client.post = AsyncMock(return_value=mock_response)

        await self.executor.execute(mock_context, mock_event_queue)

        # Should have enqueued: working status, failed status
        assert mock_event_queue.enqueue_event.call_count == 2
        last_event = mock_event_queue.enqueue_event.call_args_list[-1][0][0]
        from a2a.types import TaskState
        assert last_event.status.state == TaskState.TASK_STATE_FAILED

    @pytest.mark.asyncio
    async def test_execute_router_returns_error_field(self):
        """Test execution when router returns 200 but error in body."""
        mock_event_queue = AsyncMock()

        mock_context = MagicMock()
        mock_task = MagicMock()
        mock_task.id = "task-1"
        mock_task.context_id = "ctx-1"
        mock_context.current_task = mock_task
        mock_context.get_user_input.return_value = json.dumps({
            "service": "weather",
            "request": {"jsonrpc": "2.0", "method": "tools/list", "id": 1},
        })

        mock_response = MagicMock()
        mock_response.status_code = 200
        mock_response.json.return_value = {
            "response": None,
            "error": "Service not found: weather",
        }

        self.executor.client = AsyncMock()
        self.executor.client.post = AsyncMock(return_value=mock_response)

        await self.executor.execute(mock_context, mock_event_queue)

        # Should fail: working status + failed status
        assert mock_event_queue.enqueue_event.call_count == 2

    @pytest.mark.asyncio
    async def test_execute_unparseable_input(self):
        """Test execution with input that can't be parsed."""
        mock_event_queue = AsyncMock()

        mock_context = MagicMock()
        mock_task = MagicMock()
        mock_task.id = "task-1"
        mock_task.context_id = "ctx-1"
        mock_context.current_task = mock_task
        mock_context.get_user_input.return_value = "just some text"

        await self.executor.execute(mock_context, mock_event_queue)

        # Should fail: working status + failed status
        assert mock_event_queue.enqueue_event.call_count == 2

    @pytest.mark.asyncio
    async def test_execute_no_service_in_request(self):
        """Test execution when service field is empty."""
        mock_event_queue = AsyncMock()

        mock_context = MagicMock()
        mock_task = MagicMock()
        mock_task.id = "task-1"
        mock_task.context_id = "ctx-1"
        mock_context.current_task = mock_task
        mock_context.get_user_input.return_value = json.dumps({
            "jsonrpc": "2.0",
            "method": "tools/list",
            "id": 1,
        })

        await self.executor.execute(mock_context, mock_event_queue)

        # Should fail due to empty service
        assert mock_event_queue.enqueue_event.call_count == 2

    @pytest.mark.asyncio
    async def test_execute_creates_task_when_none(self):
        """Test that a new task is created when context has no current task."""
        mock_event_queue = AsyncMock()

        mock_message = MagicMock()
        mock_context = MagicMock()
        mock_context.current_task = None
        mock_context.message = mock_message
        mock_context.get_user_input.return_value = json.dumps({
            "service": "weather",
            "request": {"jsonrpc": "2.0", "method": "tools/list", "id": 1},
        })

        mock_response = MagicMock()
        mock_response.status_code = 200
        mock_response.json.return_value = {
            "response": {"jsonrpc": "2.0", "result": {}, "id": 1},
            "error": None,
        }
        self.executor.client = AsyncMock()
        self.executor.client.post = AsyncMock(return_value=mock_response)

        with patch("a2a_serving.a2a_server.new_task_from_user_message") as mock_new_task:
            mock_task = MagicMock()
            mock_task.id = "new-task-1"
            mock_task.context_id = "new-ctx-1"
            mock_new_task.return_value = mock_task

            await self.executor.execute(mock_context, mock_event_queue)

            mock_new_task.assert_called_once_with(mock_message)
            # new_task event + working + artifact + completed = 4
            assert mock_event_queue.enqueue_event.call_count == 4

    @pytest.mark.asyncio
    async def test_execute_no_task_no_message_raises(self):
        """Test that execute raises when there's no task and no message."""
        mock_event_queue = AsyncMock()
        mock_context = MagicMock()
        mock_context.current_task = None
        mock_context.message = None

        with pytest.raises(Exception, match="No message provided"):
            await self.executor.execute(mock_context, mock_event_queue)


# ── MCPRouterAgentExecutor.cancel Tests ─────────────────────────────


class TestCancel:
    @pytest.mark.asyncio
    async def test_cancel_raises(self):
        executor = MCPRouterAgentExecutor(router_url="http://127.0.0.1:9003")
        with pytest.raises(Exception, match="Cancel not supported"):
            await executor.cancel(MagicMock(), AsyncMock())


# ── discover_skills_from_router Tests ───────────────────────────────


class TestDiscoverSkills:
    @pytest.mark.asyncio
    async def test_discover_skills_success(self):
        """Test discovering skills from router with registered services."""
        mock_response = MagicMock()
        mock_response.status_code = 200
        mock_response.json.return_value = {
            "weather": {
                "endpoint": "http://127.0.0.1:7100/mcp",
                "healthy": True,
            },
            "stocks": {
                "endpoint": "http://127.0.0.1:7200/mcp",
                "healthy": True,
            },
        }

        with patch("a2a_serving.a2a_server.httpx.AsyncClient") as mock_client_cls:
            mock_client = AsyncMock()
            mock_client.get = AsyncMock(return_value=mock_response)
            mock_client.__aenter__ = AsyncMock(return_value=mock_client)
            mock_client.__aexit__ = AsyncMock(return_value=False)
            mock_client_cls.return_value = mock_client

            skills = await discover_skills_from_router("http://127.0.0.1:9003")

        assert len(skills) == 2
        skill_ids = {s.id for s in skills}
        assert "weather" in skill_ids
        assert "stocks" in skill_ids

        for skill in skills:
            assert "mcp" in skill.tags
            assert skill.id in skill.tags
            assert len(skill.examples) > 0

    @pytest.mark.asyncio
    async def test_discover_skills_empty(self):
        """Test discovering skills when no services are registered."""
        mock_response = MagicMock()
        mock_response.status_code = 200
        mock_response.json.return_value = {}

        with patch("a2a_serving.a2a_server.httpx.AsyncClient") as mock_client_cls:
            mock_client = AsyncMock()
            mock_client.get = AsyncMock(return_value=mock_response)
            mock_client.__aenter__ = AsyncMock(return_value=mock_client)
            mock_client.__aexit__ = AsyncMock(return_value=False)
            mock_client_cls.return_value = mock_client

            skills = await discover_skills_from_router("http://127.0.0.1:9003")

        assert skills == []

    @pytest.mark.asyncio
    async def test_discover_skills_router_down(self):
        """Test graceful handling when router is unreachable."""
        with patch("a2a_serving.a2a_server.httpx.AsyncClient") as mock_client_cls:
            mock_client = AsyncMock()
            mock_client.get = AsyncMock(side_effect=httpx.ConnectError("Connection refused"))
            mock_client.__aenter__ = AsyncMock(return_value=mock_client)
            mock_client.__aexit__ = AsyncMock(return_value=False)
            mock_client_cls.return_value = mock_client

            skills = await discover_skills_from_router("http://127.0.0.1:9003")

        assert skills == []

    @pytest.mark.asyncio
    async def test_discover_skills_bad_status(self):
        """Test handling when router returns non-200 status."""
        mock_response = MagicMock()
        mock_response.status_code = 500

        with patch("a2a_serving.a2a_server.httpx.AsyncClient") as mock_client_cls:
            mock_client = AsyncMock()
            mock_client.get = AsyncMock(return_value=mock_response)
            mock_client.__aenter__ = AsyncMock(return_value=mock_client)
            mock_client.__aexit__ = AsyncMock(return_value=False)
            mock_client_cls.return_value = mock_client

            skills = await discover_skills_from_router("http://127.0.0.1:9003")

        assert skills == []


# ── create_agent_card Tests ─────────────────────────────────────────


class TestCreateAgentCard:
    @pytest.mark.asyncio
    async def test_create_card_with_skills(self):
        """Test agent card creation with discovered skills."""
        with patch("a2a_serving.a2a_server.discover_skills_from_router") as mock_discover, \
             patch("a2a_serving.a2a_server.get_peer_id", return_value="peer-abc123"):
            from a2a.types import AgentSkill
            mock_discover.return_value = [
                AgentSkill(
                    id="weather",
                    name="Weather Service",
                    description="MCP service: weather",
                    tags=["weather", "mcp"],
                    examples=['{"service": "weather", "request": {}}'],
                ),
            ]

            card = await create_agent_card("127.0.0.1", 9004, "http://127.0.0.1:9003")

        assert card.name == "MCP Router A2A Agent"
        assert len(card.skills) == 1
        assert card.skills[0].id == "weather"
        assert card.supported_interfaces[0].url == "/a2a/peer-abc123"
        assert card.supported_interfaces[0].protocol_binding == "JSONRPC"
        assert card.capabilities.streaming is True

    @pytest.mark.asyncio
    async def test_create_card_default_skill_when_no_services(self):
        """Test that a default mcp_proxy skill is added when no services exist."""
        with patch("a2a_serving.a2a_server.discover_skills_from_router") as mock_discover, \
             patch("a2a_serving.a2a_server.get_peer_id", return_value="peer-abc123"):
            mock_discover.return_value = []

            card = await create_agent_card("127.0.0.1", 9004, "http://127.0.0.1:9003")

        assert len(card.skills) == 1
        assert card.skills[0].id == "mcp_proxy"

    @pytest.mark.asyncio
    async def test_create_card_custom_name(self):
        """Test agent card with custom agent name."""
        with patch("a2a_serving.a2a_server.discover_skills_from_router") as mock_discover, \
             patch("a2a_serving.a2a_server.get_peer_id", return_value="peer-abc123"):
            mock_discover.return_value = []

            card = await create_agent_card(
                "0.0.0.0", 8080, "http://127.0.0.1:9003", name="Custom Agent"
            )

        assert card.name == "Custom Agent"
        assert card.supported_interfaces[0].url == "/a2a/peer-abc123"
