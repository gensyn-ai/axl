"""
Local message dispatcher for AXL.

The sole consumer of GET /recv on the AXL node. When a message arrives,
it copies it to every registered consumer queue. Consumers poll their
own named endpoint to dequeue messages.

Usage:
    python3 dispatcher.py --node-port 9002 --port 9100

Endpoints:
    GET /recv/<name>   Dequeue next message for consumer <name>.
                       Queue is created on first access. Returns 200
                       with JSON body, or 204 if empty.
    GET /consumers     List registered consumer names.
    GET /health        Returns 200 "ok".

Example — run dispatcher, then point two consumers at it:
    python3 dispatcher.py --node-port 9002 --port 9100
    python3 group_chat_tui.py --port 9002 --group alpha --auto --dispatcher 9100
    python3 agent_inbox.py --port 9100

Dependencies:
    pip install requests
"""

from __future__ import annotations

import argparse
import json
import queue
import sys
import threading
import time
from http.server import ThreadingHTTPServer, BaseHTTPRequestHandler

import requests

POLL_INTERVAL = 0.2
MAX_QUEUE_SIZE = 1000


class Dispatcher:
    """Thread-safe message fan-out to named consumer queues."""

    def __init__(self) -> None:
        self._queues: dict[str, queue.Queue] = {}
        self._lock = threading.Lock()
        self._message_count = 0

    def get_or_create(self, name: str) -> queue.Queue:
        with self._lock:
            if name not in self._queues:
                self._queues[name] = queue.Queue(maxsize=MAX_QUEUE_SIZE)
            return self._queues[name]

    def broadcast(self, message: dict) -> None:
        with self._lock:
            consumers = list(self._queues.items())
            self._message_count += 1
        for name, q in consumers:
            try:
                q.put_nowait(message)
            except queue.Full:
                pass

    def dequeue(self, name: str) -> dict | None:
        q = self.get_or_create(name)
        try:
            return q.get_nowait()
        except queue.Empty:
            return None

    def consumer_names(self) -> list[str]:
        with self._lock:
            return list(self._queues.keys())

    @property
    def message_count(self) -> int:
        return self._message_count


class DispatcherHandler(BaseHTTPRequestHandler):
    dispatcher: Dispatcher

    def do_POST(self) -> None:
        path = self.path.rstrip("/")
        if path == "/broadcast":
            length = int(self.headers.get("Content-Length", 0))
            body = self.rfile.read(length) if length else b""
            try:
                msg = json.loads(body)
            except (json.JSONDecodeError, ValueError):
                self._respond(400, {"error": "invalid JSON"})
                return
            self.dispatcher.broadcast(msg)
            self.send_response(200)
            self.end_headers()
        else:
            self._respond(404, {"error": "not found"})

    def do_GET(self) -> None:
        path = self.path.rstrip("/")

        if path.startswith("/recv/"):
            name = path[6:]
            if not name:
                self._respond(400, {"error": "Consumer name required"})
                return
            msg = self.dispatcher.dequeue(name)
            if msg is None:
                self.send_response(204)
                self.end_headers()
            else:
                self._respond(200, msg)

        elif path == "/consumers":
            self._respond(200, {
                "consumers": self.dispatcher.consumer_names(),
                "total_messages": self.dispatcher.message_count,
            })

        elif path == "/health":
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b"ok")

        else:
            self._respond(404, {"error": "not found"})

    def _respond(self, code: int, body: dict) -> None:
        data = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, format, *args) -> None:
        pass


def _recv_loop(node_url: str, dispatcher: Dispatcher) -> None:
    """Poll the AXL node's /recv and broadcast to all consumers."""
    while True:
        try:
            resp = requests.get(f"{node_url}/recv", timeout=5)
            if resp.status_code == 200:
                from_peer = resp.headers.get("X-From-Peer-Id", "unknown")
                try:
                    msg = json.loads(resp.content)
                except (json.JSONDecodeError, KeyError):
                    msg = {"_raw": resp.content.decode(errors="replace")}
                msg["_from_peer"] = from_peer
                dispatcher.broadcast(msg)
        except requests.exceptions.Timeout:
            pass
        except Exception:
            pass
        time.sleep(POLL_INTERVAL)


def main() -> None:
    ap = argparse.ArgumentParser(description="AXL Local Message Dispatcher")
    ap.add_argument("--node-port", type=int, default=9002, help="AXL node API port to poll")
    ap.add_argument("--port", type=int, default=9100, help="Dispatcher HTTP port")
    args = ap.parse_args()

    node_url = f"http://127.0.0.1:{args.node_port}"

    try:
        r = requests.get(f"{node_url}/topology", timeout=5)
        if r.status_code != 200:
            raise Exception("non-200")
    except Exception:
        print(f"Cannot reach AXL node at {node_url}. Is it running?")
        sys.exit(1)

    dispatcher = Dispatcher()
    DispatcherHandler.dispatcher = dispatcher

    poller = threading.Thread(
        target=_recv_loop, args=(node_url, dispatcher), daemon=True,
    )
    poller.start()

    server = ThreadingHTTPServer(("127.0.0.1", args.port), DispatcherHandler)
    print(f"Dispatcher listening on http://127.0.0.1:{args.port}")
    print(f"Polling AXL node at {node_url}/recv")
    print(f"Consumers: GET http://127.0.0.1:{args.port}/recv/<name>")
    print()

    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\nDispatcher stopped.")
        server.server_close()


if __name__ == "__main__":
    main()
