"""
OpenClaw ↔ AXL Group Chat Bridge.

Sits between the dispatcher's agent inbox and a local OpenClaw gateway.
Incoming group-chat messages are forwarded to OpenClaw as chat prompts;
responses are fan-out sent back to the AXL group so the agent appears
as a normal participant.

Full stack:
  AXL node → dispatcher → agent queue → THIS BRIDGE → OpenClaw gateway
       ↑                                      │
       └──────── fan-out POST /send ◄─────────┘

Usage:
    python3 openclaw_bridge.py \\
        --node-port 9002 \\
        --dispatcher-port 9100 \\
        --gateway http://127.0.0.1:18789 \\
        --group alpha --auto \\
        --name "My OpenClaw"

    # With explicit members and token via env var:
    OPENCLAW_GATEWAY_TOKEN=secret python3 openclaw_bridge.py \\
        --node-port 9002 \\
        --dispatcher-port 9100 \\
        --group alpha \\
        --members KEY1,KEY2 \\
        --name "Alice's OpenClaw"

Dependencies:
    pip install requests
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import time
from datetime import datetime

import requests

POLL_INTERVAL = 0.5
OPENCLAW_TIMEOUT = 120


def _topology(base_url: str) -> dict | None:
    try:
        r = requests.get(f"{base_url}/topology", timeout=5)
        return r.json() if r.status_code == 200 else None
    except Exception as e:
        print(f"  ⚡ Topology error: {e}")
        return None


def _all_peers(topo: dict) -> list[str]:
    return [
        p["public_key"]
        for p in topo.get("peers", [])
        if p.get("up") and p.get("public_key")
    ]


def _fan_out(
    node_url: str,
    members: list[str],
    our_name: str,
    our_key: str,
    group_id: str,
    text: str,
) -> int:
    """Send a group message to every member. Returns count of failures."""
    payload = json.dumps({
        "type": "group_chat",
        "group_id": group_id,
        "from": our_name,
        "from_key": our_key,
        "text": text,
    }).encode()

    failed = 0
    for key in members:
        headers = {
            "X-Destination-Peer-Id": key,
            "Content-Type": "application/octet-stream",
        }
        try:
            resp = requests.post(
                f"{node_url}/send",
                data=payload,
                headers=headers,
                timeout=10,
            )
            if resp.status_code != 200:
                failed += 1
        except Exception:
            failed += 1
    return failed


def _ask_openclaw(
    gateway_url: str,
    token: str,
    prompt: str,
    session_key: str,
    model: str,
) -> str:
    """Send a prompt to the OpenClaw chat completions endpoint."""
    headers: dict[str, str] = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    if session_key:
        headers["x-openclaw-session-key"] = session_key

    body = {
        "model": model,
        "messages": [{"role": "user", "content": prompt}],
    }

    resp = requests.post(
        f"{gateway_url}/v1/chat/completions",
        json=body,
        headers=headers,
        timeout=OPENCLAW_TIMEOUT,
    )
    resp.raise_for_status()
    data = resp.json()
    return data["choices"][0]["message"]["content"]


def _log(tag: str, text: str) -> None:
    ts = datetime.now().strftime("%H:%M:%S")
    print(f"  [{ts}] {tag} {text}")


def main() -> None:
    ap = argparse.ArgumentParser(
        description="OpenClaw ↔ AXL Group Chat Bridge",
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )

    ap.add_argument(
        "--node-port", type=int, default=int(os.environ.get("AXL_NODE_PORT", "9002")),
        help="AXL node API port (env: AXL_NODE_PORT, default: 9002)",
    )
    ap.add_argument(
        "--dispatcher-port", type=int,
        default=int(os.environ.get("AXL_DISPATCHER_PORT", "9100")),
        help="Dispatcher HTTP port (env: AXL_DISPATCHER_PORT, default: 9100)",
    )
    ap.add_argument(
        "--dispatcher-queue", type=str,
        default=os.environ.get("AXL_DISPATCHER_QUEUE", "agent"),
        help="Dispatcher queue to poll (env: AXL_DISPATCHER_QUEUE, default: agent)",
    )
    ap.add_argument(
        "--gateway", type=str,
        default=os.environ.get("OPENCLAW_GATEWAY_URL", "http://127.0.0.1:18789"),
        help="OpenClaw gateway URL (env: OPENCLAW_GATEWAY_URL, default: http://127.0.0.1:18789)",
    )
    ap.add_argument(
        "--gateway-token", type=str,
        default=os.environ.get("OPENCLAW_GATEWAY_TOKEN", ""),
        help="OpenClaw gateway auth token (env: OPENCLAW_GATEWAY_TOKEN)",
    )
    ap.add_argument(
        "--model", type=str,
        default=os.environ.get("OPENCLAW_MODEL", "openclaw/default"),
        help="OpenClaw model/agent target (env: OPENCLAW_MODEL, default: openclaw/default)",
    )
    ap.add_argument(
        "--group", type=str, required=True,
        help="Group ID to participate in",
    )
    ap.add_argument(
        "--name", type=str,
        default=os.environ.get("OPENCLAW_DISPLAY_NAME", "OpenClaw"),
        help="Display name in the group chat (env: OPENCLAW_DISPLAY_NAME, default: OpenClaw)",
    )
    ap.add_argument(
        "--members", type=str, default=None,
        help="Comma-separated member public keys for fan-out",
    )
    ap.add_argument(
        "--auto", action="store_true",
        help="Auto-discover members from topology",
    )
    ap.add_argument(
        "--system-prompt", type=str,
        default=os.environ.get("OPENCLAW_SYSTEM_PROMPT", ""),
        help="Optional system prompt prepended to each request (env: OPENCLAW_SYSTEM_PROMPT)",
    )
    ap.add_argument(
        "--trigger", type=str,
        default=os.environ.get("OPENCLAW_TRIGGER", "@openclaw"),
        help="Agent only responds to messages containing this word (env: OPENCLAW_TRIGGER, default: @openclaw). Set to empty string to respond to everything.",
    )

    args = ap.parse_args()

    node_url = f"http://127.0.0.1:{args.node_port}"
    dispatcher_url = f"http://127.0.0.1:{args.dispatcher_port}"
    recv_url = f"{dispatcher_url}/recv/{args.dispatcher_queue}"
    gateway_url = args.gateway.rstrip("/")
    token = args.gateway_token
    session_key = f"axl-group-{args.group}"

    # ── preflight checks ──────────────────────────────────

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

    try:
        health = requests.get(f"{dispatcher_url}/health", timeout=3)
        if health.status_code != 200:
            raise ConnectionError()
    except Exception:
        print(f"Cannot reach dispatcher at {dispatcher_url}. Is it running?")
        sys.exit(1)

    # ── banner ────────────────────────────────────────────

    print()
    print(f"  ╔══════════════════════════════════════════╗")
    print(f"  ║  OpenClaw ↔ AXL Bridge                   ║")
    print(f"  ╚══════════════════════════════════════════╝")
    print(f"  Name:       {args.name}")
    print(f"  Group:      {args.group}")
    print(f"  Node:       {node_url}")
    print(f"  Dispatcher: {recv_url}")
    print(f"  Gateway:    {gateway_url}")
    print(f"  Model:      {args.model}")
    trigger_display = f'"{args.trigger}"' if args.trigger else "(all messages)"
    print(f"  Members:    {len(members)} peer(s)")
    print(f"  Trigger:    {trigger_display}")
    print(f"  Our key:    {our_key[:12]}…")
    print()
    print("  Listening for messages…  (Ctrl+C to stop)")
    print()

    # ── main loop ─────────────────────────────────────────

    msg_count = 0
    err_streak = 0

    try:
        while True:
            try:
                resp = requests.get(recv_url, timeout=5)
            except requests.exceptions.ConnectionError:
                err_streak += 1
                if err_streak == 1:
                    _log("⚡", "Dispatcher unreachable, retrying…")
                time.sleep(min(err_streak * 2, 30))
                continue
            except requests.exceptions.Timeout:
                time.sleep(POLL_INTERVAL)
                continue

            err_streak = 0

            if resp.status_code != 200:
                time.sleep(POLL_INTERVAL)
                continue

            try:
                msg = resp.json()
            except (ValueError, KeyError):
                time.sleep(POLL_INTERVAL)
                continue

            if msg.get("type") != "group_chat":
                continue
            if msg.get("group_id") != args.group:
                continue
            from_key = msg.get("from_key") or msg.get("_from_peer", "")
            if from_key == our_key:
                continue

            sender = msg.get("from", "someone")
            text = msg.get("text", "")
            if not text.strip():
                continue

            if args.trigger and args.trigger.lower() not in text.lower():
                continue

            msg_count += 1
            _log("◀ IN ", f"{sender}: {text}")

            prompt = f"{sender} says: {text}"
            if args.system_prompt:
                prompt = f"[System: {args.system_prompt}]\n\n{prompt}"

            try:
                reply = _ask_openclaw(
                    gateway_url, token, prompt, session_key, args.model,
                )
            except requests.exceptions.ConnectionError:
                _log("⚡", f"OpenClaw gateway unreachable at {gateway_url}")
                continue
            except requests.exceptions.Timeout:
                _log("⚡", "OpenClaw request timed out")
                continue
            except requests.exceptions.HTTPError as e:
                _log("⚡", f"OpenClaw HTTP error: {e}")
                continue
            except Exception as e:
                _log("⚡", f"OpenClaw error: {e}")
                continue

            _log("▶ OUT", f"{args.name}: {reply[:120]}{'…' if len(reply) > 120 else ''}")

            failed = _fan_out(node_url, members, args.name, our_key, args.group, reply)
            if failed:
                _log("⚡", f"Fan-out: {failed}/{len(members)} send(s) failed")

            time.sleep(POLL_INTERVAL)

    except KeyboardInterrupt:
        print()
        _log("■", f"Bridge stopped. Handled {msg_count} message(s).")


if __name__ == "__main__":
    main()
