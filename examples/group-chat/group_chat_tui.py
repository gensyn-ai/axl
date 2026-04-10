"""
P2P Group Chat over AXL — Terminal UI.

Encrypted group chat between multiple AXL nodes. Each outgoing message
is fan-out sent to every group member individually via POST /send.
Messages carry sender identity and a group ID so receivers know who
said what and which conversation it belongs to.

Usage:
    python3 group_chat_tui.py --port 9002 --group alpha --auto
    python3 group_chat_tui.py --port 9002 --group alpha --members KEY1,KEY2
    python3 group_chat_tui.py --port 9002 --group alpha --auto --name Alice

Dependencies:
    pip install textual requests
"""

from __future__ import annotations

import argparse
import json
import random
import sys
import time
from collections.abc import Callable
from datetime import datetime

import requests
from rich.markup import escape
from textual import work
from textual.app import App, ComposeResult
from textual.binding import Binding
from textual.containers import Vertical, VerticalScroll
from textual.screen import ModalScreen
from textual.widgets import Footer, Input, Static

POLL_INTERVAL = 0.2

_ADJECTIVES = [
    "swift", "bright", "calm", "keen", "bold",
    "warm", "cool", "quick", "sharp", "fair",
]
_NOUNS = [
    "fox", "owl", "elk", "jay", "lynx",
    "wren", "pike", "hare", "moth", "finch",
]


def _random_name() -> str:
    return f"{random.choice(_ADJECTIVES)}-{random.choice(_NOUNS)}"


MEMBER_COLORS = [
    "green", "magenta", "yellow", "red", "blue",
    "bright_green", "bright_magenta", "bright_yellow",
    "bright_red", "bright_blue", "bright_cyan",
]


# ── Widgets ────────────────────────────────────────────────────


class WelcomeScreen(ModalScreen[str]):
    """Modal overlay for choosing a display name on launch."""

    DEFAULT_CSS = """
    WelcomeScreen {
        align: center middle;
    }

    #welcome-box {
        width: 56;
        height: auto;
        padding: 2 4;
        background: $boost;
        border: thick $primary;
    }

    #welcome-title {
        text-align: center;
        margin-bottom: 1;
    }

    #welcome-hint {
        color: $text-muted;
        text-align: center;
        margin-bottom: 1;
    }
    """

    def compose(self) -> ComposeResult:
        with Vertical(id="welcome-box"):
            yield Static(
                "[bold white] AXL Group Chat [/bold white]",
                id="welcome-title",
            )
            yield Static(
                "[dim]Choose a display name for this session.\n"
                "Leave blank and press Enter for a random one.[/dim]",
                id="welcome-hint",
            )
            yield Input(placeholder="Display name…", id="name-input")

    def on_mount(self) -> None:
        self.query_one("#name-input", Input).focus()

    def on_input_submitted(self, event: Input.Submitted) -> None:
        self.dismiss(event.value.strip() or _random_name())


class AgentNameScreen(ModalScreen[str]):
    """Modal for naming the OpenClaw agent when the launcher runs with --openclaw."""

    DEFAULT_CSS = """
    AgentNameScreen {
        align: center middle;
    }

    #agent-name-box {
        width: 60;
        height: auto;
        padding: 2 4;
        background: $boost;
        border: thick $primary;
    }

    #agent-name-title {
        text-align: center;
        margin-bottom: 1;
    }

    #agent-name-hint {
        color: $text-muted;
        text-align: center;
        margin-bottom: 1;
    }
    """

    def compose(self) -> ComposeResult:
        with Vertical(id="agent-name-box"):
            yield Static(
                "[bold white] Name your OpenClaw agent [/bold white]",
                id="agent-name-title",
            )
            yield Static(
                "[dim]This name appears in chat. Others summon your agent by typing @\n"
                "followed by this exact name (case-sensitive). Leave blank for a random name.[/dim]",
                id="agent-name-hint",
            )
            yield Input(placeholder="Agent display name…", id="agent-name-input")

    def on_mount(self) -> None:
        self.query_one("#agent-name-input", Input).focus()

    def on_input_submitted(self, event: Input.Submitted) -> None:
        self.dismiss(event.value.strip() or _random_name())


class HeaderPanel(Static):
    """Pinned group-info banner."""

    DEFAULT_CSS = """
    HeaderPanel {
        dock: top;
        height: auto;
        padding: 1 3;
        background: $boost;
        border-bottom: thick $primary;
    }
    """

    def __init__(
        self, our_key: str, our_name: str, group_id: str,
        member_count: int, port: int,
        openclaw_agent: str | None = None,
    ) -> None:
        self._our_key = our_key
        self._group_id = group_id
        self._member_count = member_count
        self._port = port
        self._openclaw_agent = openclaw_agent
        self._human_name = our_name
        super().__init__(self._build_content())

    def _build_content(self) -> str:
        mc = self._member_count
        name = self._human_name
        lines = [
            f"[bold white] AXL Group Chat [/bold white]\n",
            f"\n  [cyan]⬡[/cyan]  [bold]{escape(name)}[/bold]   "
            f"[dim]{self._our_key[:12]}…[/dim]  [dim]port {self._port}[/dim]\n",
            f"  [green]⬢[/green]  [bold]{escape(self._group_id)}[/bold]  "
            f"[dim]{mc} member{'s' if mc != 1 else ''}[/dim]",
        ]
        if self._openclaw_agent:
            ea = escape(self._openclaw_agent)
            lines.append(
                f"\n  [magenta]🤖[/magenta]  Agent [bold]{ea}[/bold]  "
                f"[dim]— summon with @{ea} (exact)[/dim]"
            )
        lines.append(
            "\n\n  [dim italic]Encrypted via Yggdrasil  ·  "
            "scroll ↑↓  ·  ctrl+q to quit[/dim italic]"
        )
        return "".join(lines)

    def set_name(self, name: str) -> None:
        self._human_name = name
        self.update(self._build_content())

    def set_openclaw_agent(self, agent_name: str | None) -> None:
        self._openclaw_agent = agent_name
        self.update(self._build_content())


class ChatLog(VerticalScroll):
    """Scrollable message history."""

    DEFAULT_CSS = """
    ChatLog {
        height: 1fr;
        padding: 1 3;
    }
    """


class MessageOut(Static):
    """Outgoing message bubble."""

    DEFAULT_CSS = """
    MessageOut {
        margin: 0 0 1 8;
        border-left: thick $secondary;
        padding: 0 0 0 1;
    }
    """


class MessageIn(Static):
    """Incoming message bubble."""

    DEFAULT_CSS = """
    MessageIn {
        margin: 0 8 1 0;
        border-left: thick $success;
        padding: 0 0 0 1;
    }
    """


class SystemNote(Static):
    """Centered status message."""

    DEFAULT_CSS = """
    SystemNote {
        text-align: center;
        color: $text-muted;
        margin: 0 0 1 0;
    }
    """


# ── App ────────────────────────────────────────────────────────


class GroupChatApp(App):
    """AXL Group Chat — encrypted multi-party messaging."""

    TITLE = "AXL Group Chat"

    CSS = """
    Screen {
        background: $surface;
    }

    #chat-input {
        dock: bottom;
        margin: 0 3 1 3;
    }

    #chat-input:focus {
        border: tall $accent;
    }
    """

    BINDINGS = [
        Binding("ctrl+q", "quit", "Quit", show=True, priority=True),
        Binding("escape", "quit", "Quit", show=False),
    ]

    def __init__(
        self,
        base_url: str,
        our_key: str,
        our_name: str | None,
        group_id: str,
        members: list[str],
        port: int,
        dispatcher_url: str | None = None,
        dispatcher_queue: str = "chat",
        openclaw_mode: bool = False,
        preset_agent_name: str | None = None,
        on_agent_named: Callable[[str], None] | None = None,
    ) -> None:
        super().__init__()
        self.base_url = base_url
        self.our_key = our_key
        self.our_name = our_name or ""
        self.group_id = group_id
        self.members = members
        self.port = port
        self.dispatcher_url = dispatcher_url
        self.dispatcher_queue = dispatcher_queue
        self.openclaw_mode = openclaw_mode
        self.preset_agent_name = preset_agent_name
        self.on_agent_named = on_agent_named
        self._polling = True
        self._chat_ready = False
        self._sender_colors: dict[str, str] = {}

    def _color_for(self, key: str) -> str:
        """Assign a stable color to each unique sender."""
        if key not in self._sender_colors:
            idx = len(self._sender_colors) % len(MEMBER_COLORS)
            self._sender_colors[key] = MEMBER_COLORS[idx]
        return self._sender_colors[key]

    # ── layout ─────────────────────────────────────────────

    def compose(self) -> ComposeResult:
        total = len(self.members) + 1
        placeholder = self.our_name or "…"
        header_agent = self.preset_agent_name if self.openclaw_mode else None
        yield HeaderPanel(
            self.our_key, placeholder, self.group_id, total, self.port,
            openclaw_agent=header_agent,
        )
        yield ChatLog()
        yield Input(placeholder="Type a message…", id="chat-input", disabled=True)
        yield Footer()

    def on_mount(self) -> None:
        if self.our_name:
            self._after_display_name_flow()
        else:
            self.push_screen(WelcomeScreen(), callback=self._on_display_name_chosen)

    def _on_display_name_chosen(self, name: str) -> None:
        self.our_name = name.strip() or _random_name()
        self.query_one(HeaderPanel).set_name(self.our_name)
        self._after_display_name_flow()

    def _after_display_name_flow(self) -> None:
        if self.openclaw_mode and self.preset_agent_name is None:
            self.push_screen(AgentNameScreen(), callback=self._on_agent_name_chosen)
        else:
            if self.openclaw_mode and self.preset_agent_name:
                self.query_one(HeaderPanel).set_openclaw_agent(self.preset_agent_name)
            self._activate_chat()

    def _on_agent_name_chosen(self, name: str) -> None:
        agent = name.strip() or _random_name()
        self.query_one(HeaderPanel).set_openclaw_agent(agent)
        if self.on_agent_named:
            self.on_agent_named(agent)
        self._activate_chat()

    def _activate_chat(self) -> None:
        self._chat_ready = True
        total = len(self.members) + 1
        mode = "via dispatcher" if self.dispatcher_url else "direct"
        self._sys(
            f"Joined \"{self.group_id}\" as {self.our_name} — {total} members, "
            f"E2E encrypted via Yggdrasil. Recv: {mode}."
        )
        for m in self.members:
            self._sys(f"Member: {m[:12]}…")
        chat_input = self.query_one("#chat-input", Input)
        chat_input.disabled = False
        chat_input.focus()
        self._start_recv()

    # ── message rendering ──────────────────────────────────

    def _scroll_to_bottom(self) -> None:
        self.query_one(ChatLog).scroll_end(animate=False)

    def _out(self, text: str) -> None:
        ts = datetime.now().strftime("%H:%M:%S")
        content = (
            f"[cyan]▶[/cyan] [bold]{escape(self.our_name)}[/bold] (you)  "
            f"[dim]{ts}[/dim]\n  {escape(text)}"
        )
        self.query_one(ChatLog).mount(MessageOut(content))
        self.set_timer(0.05, self._scroll_to_bottom)

    def _in(self, text: str, sender_name: str, sender_key: str) -> None:
        ts = datetime.now().strftime("%H:%M:%S")
        color = self._color_for(sender_key)
        short = sender_key[:6]
        display = sender_name if sender_name else short
        content = (
            f"[{color}]◀[/{color}] [bold]{escape(display)}[/bold]  "
            f"[dim]{short} · {ts}[/dim]\n  {escape(text)}"
        )
        self.query_one(ChatLog).mount(MessageIn(content))
        self.set_timer(0.05, self._scroll_to_bottom)

    def _sys(self, text: str) -> None:
        content = f"[dim italic]— {escape(text)} —[/dim italic]"
        self.query_one(ChatLog).mount(SystemNote(content))
        self.set_timer(0.05, self._scroll_to_bottom)

    # ── send path (fan-out to all members) ─────────────────

    async def on_input_submitted(self, event: Input.Submitted) -> None:
        if event.input.id != "chat-input":
            return
        if not self._chat_ready:
            return
        text = event.value.strip()
        if not text:
            return
        event.input.value = ""
        self._out(text)
        self._fan_out(text)

    @work(thread=True)
    def _fan_out(self, text: str) -> None:
        payload = json.dumps({
            "type": "group_chat",
            "group_id": self.group_id,
            "from": self.our_name,
            "from_key": self.our_key,
            "text": text,
        }).encode()

        failed = 0
        for member_key in self.members:
            headers = {
                "X-Destination-Peer-Id": member_key,
                "Content-Type": "application/octet-stream",
            }
            try:
                resp = requests.post(
                    f"{self.base_url}/send",
                    data=payload,
                    headers=headers,
                    timeout=10,
                )
                if resp.status_code != 200:
                    failed += 1
            except Exception:
                failed += 1

        if failed:
            self.call_from_thread(
                self._sys,
                f"⚡ Failed to reach {failed}/{len(self.members)} member(s)",
            )

        if self.dispatcher_url:
            try:
                requests.post(
                    f"{self.dispatcher_url}/broadcast",
                    data=payload,
                    headers={"Content-Type": "application/json"},
                    timeout=5,
                )
            except Exception:
                pass

    # ── recv path ──────────────────────────────────────────

    @work(thread=True, group="recv")
    def _start_recv(self) -> None:
        if self.dispatcher_url:
            recv_url = f"{self.dispatcher_url}/recv/{self.dispatcher_queue}"
        else:
            recv_url = f"{self.base_url}/recv"

        while self._polling:
            try:
                resp = requests.get(recv_url, timeout=5)
                if resp.status_code == 200:
                    try:
                        msg = json.loads(resp.content)
                    except (json.JSONDecodeError, KeyError):
                        continue

                    if msg.get("type") != "group_chat":
                        continue
                    if msg.get("group_id") != self.group_id:
                        continue
                    if msg.get("from_key") == self.our_key and not msg.get("_from_agent"):
                        continue

                    text = msg.get("text", "")
                    sender_name = msg.get("from", "")
                    sender_key = msg.get("from_key", "unknown")
                    self.call_from_thread(self._in, text, sender_name, sender_key)
            except requests.exceptions.Timeout:
                pass
            except Exception:
                pass
            time.sleep(POLL_INTERVAL)

    # ── lifecycle ──────────────────────────────────────────

    def action_quit(self) -> None:
        self._polling = False
        self.exit()


# ── CLI ────────────────────────────────────────────────────────


def _topology(base_url: str):
    try:
        r = requests.get(f"{base_url}/topology", timeout=5)
        return r.json() if r.status_code == 200 else None
    except Exception as e:
        print(f"Topology error: {e}")
        return None


def _all_peers(topo: dict) -> list[str]:
    return [
        p["public_key"]
        for p in topo.get("peers", [])
        if p.get("up") and p.get("public_key")
    ]


def main() -> None:
    ap = argparse.ArgumentParser(description="P2P Group Chat over AXL (TUI)")
    ap.add_argument("--port", type=int, default=9002, help="AXL API port")
    ap.add_argument("--name", type=str, default=None, help="Display name (prompts in TUI if omitted)")
    ap.add_argument("--group", type=str, default="general", help="Group ID (default: general)")
    ap.add_argument("--members", type=str, help="Comma-separated member public keys")
    ap.add_argument("--auto", action="store_true", help="Auto-add all connected peers")
    ap.add_argument("--dispatcher", type=int, default=None, help="Dispatcher port (reads from dispatcher instead of /recv)")
    ap.add_argument("--dispatcher-queue", type=str, default="chat", help="Dispatcher queue name (default: chat)")
    args = ap.parse_args()

    base = f"http://127.0.0.1:{args.port}"
    topo = _topology(base)
    if not topo:
        print(f"Cannot reach node at {base}. Is it running?")
        sys.exit(1)

    our_key = topo["our_public_key"]

    if args.members:
        members = [k.strip() for k in args.members.split(",") if k.strip()]
    elif args.auto:
        members = _all_peers(topo)
        if not members:
            print("No connected peers found.")
            sys.exit(1)
    else:
        print("Provide --members KEY1,KEY2 or use --auto.")
        sys.exit(1)

    members = [k for k in members if k != our_key]

    if not members:
        print("No other members in the group (only found self).")
        sys.exit(1)

    dispatcher_url = f"http://127.0.0.1:{args.dispatcher}" if args.dispatcher else None

    GroupChatApp(base, our_key, args.name, args.group, members, args.port, dispatcher_url, args.dispatcher_queue).run()


if __name__ == "__main__":
    main()
