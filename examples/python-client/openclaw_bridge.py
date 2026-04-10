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
from collections import deque
from collections.abc import Sequence
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
    from_agent: bool = False,
) -> tuple[int, dict]:
    """Send a group message to every member. Returns (failure count, message dict)."""
    msg: dict = {
        "type": "group_chat",
        "group_id": group_id,
        "from": our_name,
        "from_key": our_key,
        "text": text,
    }
    if from_agent:
        msg["_from_agent"] = True
    payload = json.dumps(msg).encode()

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
    return failed, msg


def _strip_agent_mention(text: str, agent_name: str) -> str:
    """Remove @agent_name from text sent to the gateway.

    Otherwise the model often treats it like an internal OpenClaw session id and
    replies with "no such session" disclaimers before answering.
    """
    if not agent_name.strip():
        return text
    needle = f"@{agent_name}"
    out = text.replace(needle, "")
    return " ".join(out.split())


def _context_max_messages() -> int:
    try:
        v = int(os.environ.get("OPENCLAW_CONTEXT_MAX_MESSAGES", "80"))
        return max(5, min(v, 500))
    except ValueError:
        return 80


def new_transcript_buffer() -> deque[tuple[str, str]]:
    """Rolling buffer of (sender, text) for prompts; cap via OPENCLAW_CONTEXT_MAX_MESSAGES."""
    return deque(maxlen=_context_max_messages())


def build_openclaw_group_prompt(
    sender: str,
    raw_text: str,
    agent_name: str,
    system_prompt: str = "",
    *,
    transcript: Sequence[tuple[str, str]],
    respond_to_all: bool = False,
) -> str:
    """User message for /v1/chat/completions — full recent thread + instructions.

    Every human message is appended to ``transcript`` in the bridge loop. We only *call*
    OpenClaw on @mention (or every message if respond_to_all), but each call includes
    this transcript so the model can use messages that were never individually forwarded.
    """
    rows = list(transcript)
    if not rows:
        rows = [(sender, raw_text)]

    lines_block = "Recent group messages (oldest first):\n"
    for s, t in rows:
        lines_block += f"{s}: {t}\n"
    lines_block += "\n"

    body_stripped = _strip_agent_mention(raw_text, agent_name).strip()

    if respond_to_all:
        tail = (
            f'You are "{agent_name}". Respond to the **last** message only, '
            "using all lines above as context. Be concise.\n"
            f'Do not claim you are offline or that "{agent_name}" does not exist.\n'
        )
    else:
        tail = (
            f'You are "{agent_name}". Only the **last** message @-mentioned you; '
            "earlier lines are background you may use (including messages that did not ping you). "
            "Answer what the last line asks, using that full context.\n"
            f'Do not claim you are offline or that "{agent_name}" does not exist.\n'
        )

    if body_stripped:
        tail += f"\n(Last line with @-tag stripped for clarity: {body_stripped})\n"

    out = lines_block + tail
    if system_prompt:
        return f"[System: {system_prompt}]\n\n{out}"
    return out


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
        "--openclaw-respond-all", action="store_true",
        help="Respond to every message (no @mention). Default: only when the message contains "
        "@NAME exactly (NAME is --name, case-sensitive substring). Env: OPENCLAW_RESPOND_ALL=1",
    )

    args = ap.parse_args()
    respond_to_all = args.openclaw_respond_all or (
        os.environ.get("OPENCLAW_RESPOND_ALL", "").strip().lower()
        in ("1", "true", "yes")
    )

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
    mention = f"@{args.name}"
    filter_display = (
        "every message (--openclaw-respond-all)"
        if respond_to_all
        else f'message contains "{mention}" exactly (case-sensitive)'
    )
    print(f"  Members:    {len(members)} peer(s)")
    print(f"  Replies to: {filter_display}")
    print(f"  Our key:    {our_key[:12]}…")
    print()
    print("  Listening for messages…  (Ctrl+C to stop)")
    print()

    # ── main loop ─────────────────────────────────────────

    msg_count = 0
    err_streak = 0
    history = new_transcript_buffer()

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
            if msg.get("_from_agent"):
                continue

            sender = msg.get("from", "someone")
            text = msg.get("text", "")
            if not text.strip():
                continue

            history.append((sender, text))

            if not respond_to_all:
                if f"@{args.name}" not in text:
                    continue

            msg_count += 1
            _log("◀ IN ", f"{sender}: {text}")

            prompt = build_openclaw_group_prompt(
                sender,
                text,
                args.name,
                args.system_prompt,
                transcript=list(history),
                respond_to_all=respond_to_all,
            )

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

            failed, response_msg = _fan_out(
                node_url, members, args.name, our_key, args.group, reply,
                from_agent=True,
            )
            if failed:
                _log("⚡", f"Fan-out: {failed}/{len(members)} send(s) failed")

            try:
                requests.post(
                    f"{dispatcher_url}/broadcast",
                    json=response_msg,
                    timeout=5,
                )
            except Exception:
                pass

            time.sleep(POLL_INTERVAL)

    except KeyboardInterrupt:
        print()
        _log("■", f"Bridge stopped. Handled {msg_count} message(s).")


if __name__ == "__main__":
    main()
