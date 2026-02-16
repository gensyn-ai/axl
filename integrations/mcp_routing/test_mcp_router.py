"""Unit tests for the MCP Router Service."""

import asyncio
from unittest.mock import AsyncMock, patch, MagicMock

import pytest
from aiohttp import web

import mcp_routing.mcp_router as mcp_router


@pytest.fixture
def app():
    """Create aiohttp app with router endpoints."""
    application = web.Application()
    application.router.add_post("/route", mcp_router.handle_route)
    application.router.add_post("/register", mcp_router.handle_register)
    application.router.add_delete("/register/{service}", mcp_router.handle_deregister)
    application.router.add_get("/services", mcp_router.handle_services)
    application.router.add_get("/health", mcp_router.handle_health)
    return application


@pytest.fixture(autouse=True)
def clear_services():
    """Clear the service registry before each test."""
    mcp_router.services.clear()
    yield
    mcp_router.services.clear()


# ── Registration Tests ──────────────────────────────────────────────


class TestRegister:
    async def test_register_service(self, aiohttp_client, app):
        client = await aiohttp_client(app)
        resp = await client.post("/register", json={
            "service": "weather",
            "endpoint": "http://127.0.0.1:7100/mcp",
        })
        assert resp.status == 200
        data = await resp.json()
        assert data["status"] == "registered"
        assert data["service"] == "weather"
        assert "weather" in mcp_router.services
        assert mcp_router.services["weather"]["endpoint"] == "http://127.0.0.1:7100/mcp"
        assert mcp_router.services["weather"]["healthy"] is True

    async def test_register_missing_service_name(self, aiohttp_client, app):
        client = await aiohttp_client(app)
        resp = await client.post("/register", json={
            "endpoint": "http://127.0.0.1:7100/mcp",
        })
        assert resp.status == 400
        data = await resp.json()
        assert "required" in data["error"].lower()

    async def test_register_missing_endpoint(self, aiohttp_client, app):
        client = await aiohttp_client(app)
        resp = await client.post("/register", json={
            "service": "weather",
        })
        assert resp.status == 400

    async def test_register_invalid_json(self, aiohttp_client, app):
        client = await aiohttp_client(app)
        resp = await client.post("/register", data=b"not-json",
                                 headers={"Content-Type": "application/json"})
        assert resp.status == 400
        data = await resp.json()
        assert "Invalid JSON" in data["error"]

    async def test_register_overwrites_existing(self, aiohttp_client, app):
        client = await aiohttp_client(app)
        await client.post("/register", json={
            "service": "weather",
            "endpoint": "http://127.0.0.1:7100/mcp",
        })
        await client.post("/register", json={
            "service": "weather",
            "endpoint": "http://127.0.0.1:7200/mcp",
        })
        assert mcp_router.services["weather"]["endpoint"] == "http://127.0.0.1:7200/mcp"


# ── Deregistration Tests ────────────────────────────────────────────


class TestDeregister:
    async def test_deregister_service(self, aiohttp_client, app):
        client = await aiohttp_client(app)
        await client.post("/register", json={
            "service": "weather",
            "endpoint": "http://127.0.0.1:7100/mcp",
        })
        resp = await client.delete("/register/weather")
        assert resp.status == 200
        data = await resp.json()
        assert data["status"] == "deregistered"
        assert "weather" not in mcp_router.services

    async def test_deregister_nonexistent(self, aiohttp_client, app):
        client = await aiohttp_client(app)
        resp = await client.delete("/register/nonexistent")
        assert resp.status == 404
        data = await resp.json()
        assert "not found" in data["error"].lower()


# ── Services List Tests ─────────────────────────────────────────────


class TestServicesList:
    async def test_services_empty(self, aiohttp_client, app):
        client = await aiohttp_client(app)
        resp = await client.get("/services")
        assert resp.status == 200
        data = await resp.json()
        assert data == {}

    async def test_services_with_registered(self, aiohttp_client, app):
        client = await aiohttp_client(app)
        await client.post("/register", json={
            "service": "weather",
            "endpoint": "http://127.0.0.1:7100/mcp",
        })
        await client.post("/register", json={
            "service": "stocks",
            "endpoint": "http://127.0.0.1:7200/mcp",
        })
        resp = await client.get("/services")
        data = await resp.json()
        assert "weather" in data
        assert "stocks" in data
        assert data["weather"]["endpoint"] == "http://127.0.0.1:7100/mcp"


# ── Health Check Tests ──────────────────────────────────────────────


class TestHealth:
    async def test_health(self, aiohttp_client, app):
        client = await aiohttp_client(app)
        resp = await client.get("/health")
        assert resp.status == 200
        data = await resp.json()
        assert data["status"] == "ok"
        assert data["service_count"] == 0

    async def test_health_with_services(self, aiohttp_client, app):
        client = await aiohttp_client(app)
        await client.post("/register", json={
            "service": "weather",
            "endpoint": "http://127.0.0.1:7100/mcp",
        })
        resp = await client.get("/health")
        data = await resp.json()
        assert data["service_count"] == 1


# ── Routing Tests ───────────────────────────────────────────────────


class TestRoute:
    async def test_route_missing_service_field(self, aiohttp_client, app):
        client = await aiohttp_client(app)
        resp = await client.post("/route", json={
            "request": {"jsonrpc": "2.0", "method": "tools/list", "id": 1},
        })
        assert resp.status == 400
        data = await resp.json()
        assert "Missing 'service'" in data["error"]

    async def test_route_service_not_found(self, aiohttp_client, app):
        client = await aiohttp_client(app)
        resp = await client.post("/route", json={
            "service": "nonexistent",
            "request": {"jsonrpc": "2.0", "method": "tools/list", "id": 1},
        })
        assert resp.status == 404
        data = await resp.json()
        assert "not found" in data["error"].lower()

    async def test_route_invalid_json(self, aiohttp_client, app):
        client = await aiohttp_client(app)
        resp = await client.post("/route", data=b"not-json",
                                 headers={"Content-Type": "application/json"})
        assert resp.status == 400
        data = await resp.json()
        assert "Invalid JSON" in data["error"]

    async def test_route_success(self, aiohttp_client, app, monkeypatch):
        """Test successful routing to a registered service."""
        client = await aiohttp_client(app)

        await client.post("/register", json={
            "service": "weather",
            "endpoint": "http://127.0.0.1:7100/mcp",
        })

        mock_response = AsyncMock()
        mock_response.status = 200
        mock_response.json = AsyncMock(return_value={
            "jsonrpc": "2.0",
            "result": {"tools": []},
            "id": 1,
        })

        # session.post() returns an async context manager synchronously in aiohttp
        mock_post_cm = MagicMock()
        mock_post_cm.__aenter__ = AsyncMock(return_value=mock_response)
        mock_post_cm.__aexit__ = AsyncMock(return_value=False)

        mock_session = MagicMock()
        mock_session.post.return_value = mock_post_cm
        mock_session.__aenter__ = AsyncMock(return_value=mock_session)
        mock_session.__aexit__ = AsyncMock(return_value=False)

        monkeypatch.setattr(mcp_router, "ClientSession", lambda **kw: mock_session)

        resp = await client.post("/route", json={
            "service": "weather",
            "request": {"jsonrpc": "2.0", "method": "tools/list", "id": 1},
            "from_peer_id": "test-peer-1234567890",
        })

        assert resp.status == 200
        data = await resp.json()
        assert data["error"] is None
        assert data["response"]["jsonrpc"] == "2.0"
        assert mcp_router.services["weather"]["healthy"] is True

    async def test_route_204_no_content(self, aiohttp_client, app, monkeypatch):
        """Test routing when downstream returns 204 (notification)."""
        client = await aiohttp_client(app)

        await client.post("/register", json={
            "service": "weather",
            "endpoint": "http://127.0.0.1:7100/mcp",
        })

        mock_response = AsyncMock()
        mock_response.status = 204

        mock_post_cm = MagicMock()
        mock_post_cm.__aenter__ = AsyncMock(return_value=mock_response)
        mock_post_cm.__aexit__ = AsyncMock(return_value=False)

        mock_session = MagicMock()
        mock_session.post.return_value = mock_post_cm
        mock_session.__aenter__ = AsyncMock(return_value=mock_session)
        mock_session.__aexit__ = AsyncMock(return_value=False)

        monkeypatch.setattr(mcp_router, "ClientSession", lambda **kw: mock_session)

        resp = await client.post("/route", json={
            "service": "weather",
            "request": {"jsonrpc": "2.0", "method": "notifications/initialized"},
        })

        assert resp.status == 200
        data = await resp.json()
        assert data["response"] is None
        assert data["error"] is None

    async def test_route_service_error(self, aiohttp_client, app, monkeypatch):
        """Test routing when downstream returns an error status."""
        client = await aiohttp_client(app)

        await client.post("/register", json={
            "service": "weather",
            "endpoint": "http://127.0.0.1:7100/mcp",
        })

        mock_response = AsyncMock()
        mock_response.status = 500
        mock_response.text = AsyncMock(return_value="Internal Server Error")

        mock_post_cm = MagicMock()
        mock_post_cm.__aenter__ = AsyncMock(return_value=mock_response)
        mock_post_cm.__aexit__ = AsyncMock(return_value=False)

        mock_session = MagicMock()
        mock_session.post.return_value = mock_post_cm
        mock_session.__aenter__ = AsyncMock(return_value=mock_session)
        mock_session.__aexit__ = AsyncMock(return_value=False)

        monkeypatch.setattr(mcp_router, "ClientSession", lambda **kw: mock_session)

        resp = await client.post("/route", json={
            "service": "weather",
            "request": {"jsonrpc": "2.0", "method": "tools/list", "id": 1},
        })

        assert resp.status == 502
        data = await resp.json()
        assert "Service error" in data["error"]
        assert mcp_router.services["weather"]["healthy"] is False

    async def test_route_timeout(self, aiohttp_client, app, monkeypatch):
        """Test routing when downstream times out."""
        client = await aiohttp_client(app)

        await client.post("/register", json={
            "service": "weather",
            "endpoint": "http://127.0.0.1:7100/mcp",
        })

        # TimeoutError is raised inside the ClientSession context manager,
        # so we make post() raise when called
        mock_session = MagicMock()
        mock_session.post.side_effect = asyncio.TimeoutError()
        mock_session.__aenter__ = AsyncMock(return_value=mock_session)
        mock_session.__aexit__ = AsyncMock(return_value=False)

        monkeypatch.setattr(mcp_router, "ClientSession", lambda **kw: mock_session)

        resp = await client.post("/route", json={
            "service": "weather",
            "request": {"jsonrpc": "2.0", "method": "tools/list", "id": 1},
        })

        assert resp.status == 504
        data = await resp.json()
        assert "timeout" in data["error"].lower()
        assert mcp_router.services["weather"]["healthy"] is False

    async def test_route_connection_error(self, aiohttp_client, app, monkeypatch):
        """Test routing when downstream connection fails."""
        client = await aiohttp_client(app)

        await client.post("/register", json={
            "service": "weather",
            "endpoint": "http://127.0.0.1:7100/mcp",
        })

        mock_session = MagicMock()
        mock_session.post.side_effect = ConnectionRefusedError("Connection refused")
        mock_session.__aenter__ = AsyncMock(return_value=mock_session)
        mock_session.__aexit__ = AsyncMock(return_value=False)

        monkeypatch.setattr(mcp_router, "ClientSession", lambda **kw: mock_session)

        resp = await client.post("/route", json={
            "service": "weather",
            "request": {"jsonrpc": "2.0", "method": "tools/list", "id": 1},
        })

        assert resp.status == 502
        data = await resp.json()
        assert "Forward error" in data["error"]
        assert mcp_router.services["weather"]["healthy"] is False
