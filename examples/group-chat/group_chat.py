"""
AXL Group Chat — unified launcher.

One command for everything: TUI, dispatcher, and optional OpenClaw bridge.

    # Human-only group chat (no dispatcher, no AI):
    python3 group_chat.py --port 9002 --group alpha --auto

    # Human + your OpenClaw agent (dispatcher + bridge spun up automatically):
    python3 group_chat.py --port 9002 --group alpha --auto --openclaw

    # With custom names and gateway token:
    python3 group_chat.py --port 9002 --group alpha --auto \\
        --name Alice --openclaw --agent-name "Alice's OpenClaw" \\
        --gateway-token mytoken

Dependencies:
    pip install textual requests
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import threading
import time
from http.server import ThreadingHTTPServer

import requests

from dispatcher import Dispatcher, DispatcherHandler, _recv_loop
from group_chat_tui import GroupChatApp, _topology, _all_peers
from openclaw_bridge import _ask_openclaw, _fan_out as _bridge_fan_out

BRIDGE_POLL = 0.5


def _start_dispatcher_background(node_url: str) -> int:
    """Start the dispatcher HTTP server + recv poller as daemon threads.

    Returns the port the dispatcher is listening on.
    """
    disp = Dispatcher()
    DispatcherHandler.dispatcher = disp

    server = ThreadingHTTPServer(("127.0.0.1", 0), DispatcherHandler)
    port = server.server_address[1]

    threading.Thread(
        target=_recv_loop, args=(node_url, disp), daemon=True,
    ).start()

    threading.Thread(
        target=server.serve_forever, daemon=True,
    ).start()

    return port


def _bridge_loop(
    dispatcher_url: str,
    queue_name: str,
    node_url: str,
    gateway_url: str,
    token: str,
    model: str,
    session_key: str,
    system_prompt: str,
    our_key: str,
    agent_name: str,
    group_id: str,
    members: list[str],
    trigger: str = "",
) -> None:
    """Poll the dispatcher's agent queue and forward to OpenClaw.

    Runs forever in a daemon thread.
    """
    recv_url = f"{dispatcher_url}/recv/{queue_name}"

    while True:
        try:
            resp = requests.get(recv_url, timeout=5)
        except requests.exceptions.ConnectionError:
            time.sleep(2)
            continue
        except requests.exceptions.Timeout:
            time.sleep(BRIDGE_POLL)
            continue

        if resp.status_code != 200:
            time.sleep(BRIDGE_POLL)
            continue

        try:
            msg = resp.json()
        except (ValueError, KeyError):
            time.sleep(BRIDGE_POLL)
            continue

        if msg.get("type") != "group_chat":
            continue
        if msg.get("group_id") != group_id:
            continue
        if msg.get("_from_agent"):
            continue

        text = msg.get("text", "")
        if not text.strip():
            continue

        if trigger and trigger.lower() not in text.lower():
            continue

        sender = msg.get("from", "someone")
        prompt = f"{sender} says: {text}"
        if system_prompt:
            prompt = f"[System: {system_prompt}]\n\n{prompt}"

        try:
            reply = _ask_openclaw(gateway_url, token, prompt, session_key, model)
        except Exception:
            continue

        failed, response_msg = _bridge_fan_out(
            node_url, members, agent_name, our_key, group_id, reply,
            from_agent=True,
        )

        try:
            requests.post(
                f"{dispatcher_url}/broadcast",
                json=response_msg,
                timeout=5,
            )
        except Exception:
            pass

        time.sleep(BRIDGE_POLL)


def main() -> None:
    ap = argparse.ArgumentParser(
        description="AXL Group Chat (with optional OpenClaw agent)",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=(
            "Examples:\n"
            "  python3 group_chat.py --port 9002 --group alpha --auto\n"
            "  python3 group_chat.py --port 9002 --group alpha --auto --openclaw\n"
            "  python3 group_chat.py --port 9002 --group alpha --auto "
            '--openclaw --agent-name "My OpenClaw"\n'
        ),
    )

    grp_core = ap.add_argument_group("core")
    grp_core.add_argument(
        "--port", type=int,
        default=int(os.environ.get("AXL_NODE_PORT", "9002")),
        help="AXL node API port (env: AXL_NODE_PORT, default: 9002)",
    )
    grp_core.add_argument(
        "--name", type=str, default=None,
        help="Your display name (prompts in TUI if omitted)",
    )
    grp_core.add_argument(
        "--group", type=str, default="general",
        help="Group ID (default: general)",
    )
    grp_core.add_argument(
        "--members", type=str, default=None,
        help="Comma-separated member public keys",
    )
    grp_core.add_argument(
        "--auto", action="store_true",
        help="Auto-discover all connected peers",
    )

    grp_oc = ap.add_argument_group("openclaw")
    grp_oc.add_argument(
        "--openclaw", action="store_true",
        help="Enable OpenClaw agent (starts dispatcher + bridge automatically)",
    )
    grp_oc.add_argument(
        "--agent-name", type=str,
        default=os.environ.get("OPENCLAW_DISPLAY_NAME", "OpenClaw"),
        help="Agent display name in chat (env: OPENCLAW_DISPLAY_NAME, default: OpenClaw)",
    )
    grp_oc.add_argument(
        "--gateway", type=str,
        default=os.environ.get("OPENCLAW_GATEWAY_URL", "http://127.0.0.1:18789"),
        help="OpenClaw gateway URL (env: OPENCLAW_GATEWAY_URL, default: http://127.0.0.1:18789)",
    )
    grp_oc.add_argument(
        "--gateway-token", type=str,
        default=os.environ.get("OPENCLAW_GATEWAY_TOKEN", ""),
        help="Gateway auth token (env: OPENCLAW_GATEWAY_TOKEN)",
    )
    grp_oc.add_argument(
        "--model", type=str,
        default=os.environ.get("OPENCLAW_MODEL", "openclaw/default"),
        help="OpenClaw model/agent target (env: OPENCLAW_MODEL, default: openclaw/default)",
    )
    grp_oc.add_argument(
        "--system-prompt", type=str,
        default=os.environ.get("OPENCLAW_SYSTEM_PROMPT", ""),
        help="System prompt for the agent (env: OPENCLAW_SYSTEM_PROMPT)",
    )
    grp_oc.add_argument(
        "--trigger", type=str,
        default=os.environ.get("OPENCLAW_TRIGGER", "@openclaw"),
        help="Agent only responds to messages containing this word (env: OPENCLAW_TRIGGER, default: @openclaw). Set to empty string to respond to everything.",
    )

    args = ap.parse_args()

    # ── preflight ─────────────────────────────────────────

    node_url = f"http://127.0.0.1:{args.port}"
    topo = _topology(node_url)
    if not topo:
        print(f"Cannot reach AXL node at {node_url}. Is it running?")
        sys.exit(1)

    our_key = topo["our_public_key"]

    if args.members:
        members = [k.strip() for k in args.members.split(",") if k.strip()]
    elif args.auto:
        members = _all_peers(topo)
        if not members:
            print("No connected peers found via --auto.")
            sys.exit(1)
    else:
        print("Provide --members KEY1,KEY2 or use --auto.")
        sys.exit(1)

    members = [k for k in members if k != our_key]
    if not members:
        print("No other members in the group (only found self).")
        sys.exit(1)

    # ── optional openclaw stack ───────────────────────────

    dispatcher_url: str | None = None
    dispatcher_queue = "chat"

    if args.openclaw:
        gateway_url = args.gateway.rstrip("/")
        token = args.gateway_token

        check_headers: dict[str, str] = {}
        if token:
            check_headers["Authorization"] = f"Bearer {token}"

        try:
            r = requests.get(
                f"{gateway_url}/v1/models", headers=check_headers, timeout=5,
            )
            if r.status_code == 401:
                print(
                    f"OpenClaw gateway at {gateway_url} returned 401 (Unauthorized).\n"
                    "The gateway requires an auth token. Pass it via:\n\n"
                    "  --gateway-token YOUR_TOKEN\n"
                    "  or set OPENCLAW_GATEWAY_TOKEN=YOUR_TOKEN\n\n"
                    "Find your token in ~/.openclaw/openclaw.json under\n"
                    "gateway.auth.token, or run:  openclaw token\n"
                )
                sys.exit(1)
            elif r.status_code >= 400:
                print(
                    f"OpenClaw gateway at {gateway_url} returned {r.status_code}.\n"
                    "Make sure the chatCompletions endpoint is enabled in\n"
                    "~/.openclaw/openclaw.json:\n\n"
                    '  { gateway: { http: { endpoints: { chatCompletions: { enabled: true } } } } }\n'
                )
                sys.exit(1)
        except requests.exceptions.ConnectionError:
            print(
                f"Cannot reach OpenClaw gateway at {gateway_url}.\n"
                "Is OpenClaw running?  (openclaw onboard --install-daemon)\n"
            )
            sys.exit(1)

        disp_port = _start_dispatcher_background(node_url)
        dispatcher_url = f"http://127.0.0.1:{disp_port}"

        session_key = f"axl-group-{args.group}"

        threading.Thread(
            target=_bridge_loop,
            kwargs=dict(
                dispatcher_url=dispatcher_url,
                queue_name="agent",
                node_url=node_url,
                gateway_url=gateway_url,
                token=token,
                model=args.model,
                session_key=session_key,
                system_prompt=args.system_prompt,
                our_key=our_key,
                agent_name=args.agent_name,
                group_id=args.group,
                members=members,
                trigger=args.trigger,
            ),
            daemon=True,
        ).start()

        trigger_display = f'"{args.trigger}"' if args.trigger else "(all messages)"
        print(f"  OpenClaw enabled: {args.agent_name}")
        print(f"  Gateway: {gateway_url}  Model: {args.model}")
        print(f"  Trigger: {trigger_display}")
        print(f"  Dispatcher: port {disp_port} (auto)")
        print()

    # ── launch TUI ────────────────────────────────────────

    GroupChatApp(
        base_url=node_url,
        our_key=our_key,
        our_name=args.name,
        group_id=args.group,
        members=members,
        port=args.port,
        dispatcher_url=dispatcher_url,
        dispatcher_queue=dispatcher_queue,
    ).run()


if __name__ == "__main__":
    main()
