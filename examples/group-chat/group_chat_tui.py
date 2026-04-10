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
from collections.abc import Callable, Iterable
from datetime import datetime

import requests
from rich.markdown import Markdown
from rich.markup import escape

from textual import work
from textual.app import App, ComposeResult, SystemCommand
from textual.binding import Binding
from textual.containers import Vertical, VerticalScroll
from textual.screen import ModalScreen, Screen
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


_WELCOME_FRAME = (
    "[#8FA4B8]────────────────────────────────────────[/]\n"
    " [bold #3D5A78]* AXL[/]  [#2D4A62]P2P · Yggdrasil · E2E[/]\n"
    " [#2D4A62]Your name appears beside your messages.[/]\n"
    "[#8FA4B8]────────────────────────────────────────[/]"
)

_AGENT_FRAME = (
    "[#8FA4B8]────────────────────────────────────────[/]\n"
    " [bold #3D5A78]* OpenClaw[/]  [#2D4A62]@mention · case-sensitive[/]\n"
    " [#2D4A62]Others must type [bold]@[/][#2D4A62]exactly this label in chat.[/]\n"
    "[#8FA4B8]────────────────────────────────────────[/]"
)

_KEYBAR_MARKUP = (
    " [bold #3D5A78]^Q[/] Quit   "
    "[bold #3D5A78]^P[/] palette   "
    "[bold #3D5A78]^T[/] Theme   "
    "[bold #3D5A78]F1[/] Keys "
)


class AxlInput(Input, inherit_css=False):
    """Input with solid borders only — eliminates Textual's default tall/half-block
    focus ring which renders as neon green in many terminal palettes."""

    COMPONENT_CLASSES = {
        "input--cursor",
        "input--placeholder",
        "input--suggestion",
        "input--selection",
    }

    DEFAULT_CSS = """
    AxlInput {
        background: $surface;
        color: $foreground;
        padding: 0 2;
        border: solid $border-blurred;
        width: 100%;
        height: 3;
        scrollbar-size-horizontal: 0;
        pointer: text;

        &:focus {
            border: solid $border;
        }
        &>.input--cursor {
            background: $input-cursor-background;
            color: $input-cursor-foreground;
            text-style: $input-cursor-text-style;
        }
        &>.input--selection {
            background: $input-selection-background;
        }
        &>.input--placeholder, &>.input--suggestion {
            color: $text-disabled;
        }
        &.-invalid {
            border: solid $error 60%;
        }
        &.-invalid:focus {
            border: solid $error;
        }
    }
    """


# ── Widgets ────────────────────────────────────────────────────


class WelcomeScreen(ModalScreen[str]):
    """Win95-style setup: display name."""

    DEFAULT_CSS = """
    WelcomeScreen {
        align: center middle;
        background: #E2EEF8;
    }
    #welcome-box {
        width: 58;
        height: auto;
        background: #E2EEF8;
        border: solid #8FA4B8;
    }
    #welcome-title {
        width: 100%;
        text-align: left;
        text-style: bold;
        color: #F8FAFF;
        background: #4A6FA0;
        padding: 0 1;
    }
    #welcome-menubar {
        width: 100%;
        height: auto;
        padding: 0 1;
        background: #D2E2F0;
        color: #2A4058;
        border-bottom: solid #A8B8C8;
    }
    #welcome-banner {
        margin: 1 2 0 2;
        height: auto;
        color: #2D4A62;
        text-align: center;
    }
    #welcome-client {
        margin: 1 2 1 2;
        padding: 1 2;
        height: auto;
        background: #EEF5FB;
        border: solid #B0C0D0;
    }
    #welcome-blurb {
        color: #2A4055;
        margin-bottom: 1;
    }
    #welcome-label {
        color: #1E3A50;
        margin: 0;
        text-style: bold;
    }
    #welcome-field-sink {
        margin-top: 0;
        padding: 0;
        background: #FFFFFF;
        height: auto;
    }
    #name-input {
        border: solid #A8B8C8;
        background: #FFFFFF;
        color: #2D4A62;
        margin: 0;
    }
    #name-input:focus {
        border: solid #4A6FA0;
    }
    #welcome-footer {
        text-align: center;
        color: #4A6078;
        margin: 0 2 1 2;
        height: auto;
    }
    """

    def compose(self) -> ComposeResult:
        with Vertical(id="welcome-box"):
            yield Static(
                "  [bold white]●[/]  [bold white]AXL Group Chat[/]  "
                "[dim white]──[/]  [italic]Display name[/]  [dim white](step 1 of 2)[/]  ",
                id="welcome-title",
            )
            yield Static(
                "  [bold]F[/]ile        [bold]H[/]elp        ",
                id="welcome-menubar",
            )
            yield Static(_WELCOME_FRAME, id="welcome-banner")
            with Vertical(id="welcome-client"):
                yield Static(
                    "[bold #1E3A50]Nickname[/]\n"
                    "[#3A5068]How you appear in this group. Empty = random animal-style name.[/]",
                    id="welcome-blurb",
                )
                yield Static("Name:", id="welcome-label")
                with Vertical(id="welcome-field-sink"):
                    yield AxlInput(
                        placeholder="Type a name, or press Enter for random…",
                        id="name-input",
                    )
            yield Static(
                "[#404040]Enter[/]  accept   ·   [#404040]blank[/]  random name",
                id="welcome-footer",
            )

    def on_mount(self) -> None:
        self.query_one("#name-input", AxlInput).focus()

    def on_input_submitted(self, event: Input.Submitted) -> None:
        self.dismiss(event.value.strip() or _random_name())


class AgentNameScreen(ModalScreen[str]):
    """Win95-style setup: OpenClaw agent @name."""

    DEFAULT_CSS = """
    AgentNameScreen {
        align: center middle;
        background: #E2EEF8;
    }
    #agent-name-box {
        width: 58;
        height: auto;
        background: #E2EEF8;
        border: solid #8FA4B8;
    }
    #agent-name-title {
        width: 100%;
        text-align: left;
        text-style: bold;
        color: #F8FAFF;
        background: #4A6FA0;
        padding: 0 1;
    }
    #agent-menubar {
        width: 100%;
        height: auto;
        padding: 0 1;
        background: #D2E2F0;
        color: #2A4058;
        border-bottom: solid #A8B8C8;
    }
    #agent-banner {
        margin: 1 2 0 2;
        height: auto;
        color: #2D4A62;
        text-align: center;
    }
    #agent-client {
        margin: 1 2 1 2;
        padding: 1 2;
        height: auto;
        background: #EEF5FB;
        border: solid #B0C0D0;
    }
    #agent-blurb {
        color: #2A4055;
        margin-bottom: 1;
    }
    #agent-label {
        color: #1E3A50;
        text-style: bold;
    }
    #agent-field-sink {
        margin-top: 0;
        padding: 0;
        background: #FFFFFF;
        height: auto;
    }
    #agent-name-input {
        border: solid #A8B8C8;
        background: #FFFFFF;
        color: #2D4A62;
        margin: 0;
    }
    #agent-name-input:focus {
        border: solid #4A6FA0;
    }
    #agent-footer {
        text-align: center;
        color: #4A6078;
        margin: 0 2 1 2;
        height: auto;
    }
    """

    def compose(self) -> ComposeResult:
        with Vertical(id="agent-name-box"):
            yield Static(
                "  [bold white]●[/]  [bold white]OpenClaw agent[/]  "
                "[dim white]──[/]  [italic]@mention name[/]  [dim white](step 2 of 2)[/]  ",
                id="agent-name-title",
            )
            yield Static(
                "  [bold]F[/]ile        [bold]H[/]elp        ",
                id="agent-menubar",
            )
            yield Static(_AGENT_FRAME, id="agent-banner")
            with Vertical(id="agent-client"):
                yield Static(
                    "[bold #1E3A50]Agent label[/]\n"
                    "[#3A5068]Must match what people type after @ (case-sensitive). Empty = random.[/]",
                    id="agent-blurb",
                )
                yield Static("@name:", id="agent-label")
                with Vertical(id="agent-field-sink"):
                    yield AxlInput(
                        placeholder="e.g. guy, research-bot, …",
                        id="agent-name-input",
                    )
            yield Static(
                "[#404040]Enter[/]  accept   ·   [#404040]blank[/]  random name",
                id="agent-footer",
            )

    def on_mount(self) -> None:
        self.query_one("#agent-name-input", AxlInput).focus()

    def on_input_submitted(self, event: Input.Submitted) -> None:
        self.dismiss(event.value.strip() or _random_name())


class HeaderPanel(Vertical):
    """Session chrome: same structure as setup wizards (title bar + menu strip + framed body)."""

    DEFAULT_CSS = """
    HeaderPanel {
        dock: top;
        height: auto;
        width: 1fr;
        background: #E2EEF8;
        border-bottom: solid #8FA4B8;
    }
    HeaderPanel #header-title {
        width: 100%;
        text-align: left;
        text-style: bold;
        color: #F8FAFF;
        background: #4A6FA0;
        padding: 0 1;
    }
    HeaderPanel #header-menubar {
        width: 100%;
        height: auto;
        padding: 0 1;
        background: #D2E2F0;
        color: #2A4058;
        border-bottom: solid #A8B8C8;
    }
    HeaderPanel #header-body {
        margin: 1 2 1 2;
        padding: 1 2;
        height: auto;
        background: #EEF5FB;
        border: solid #B0C0D0;
        color: #2D4A62;
    }
    """

    def __init__(
        self,
        our_key: str,
        our_name: str,
        group_id: str,
        member_count: int,
        port: int,
        member_keys: list[str],
        openclaw_agent: str | None = None,
    ) -> None:
        self._our_key = our_key
        self._group_id = group_id
        self._member_count = member_count
        self._port = port
        self._member_keys = list(member_keys)
        self._openclaw_agent = openclaw_agent
        self._human_name = our_name
        self._session_ready = False
        self._recv_mode: str | None = None
        super().__init__()

    def compose(self) -> ComposeResult:
        yield Static(
            "  [bold white]●[/]  [bold white]AXL Group Chat[/]  "
            "[dim white]──[/]  [italic]Session[/]  ",
            id="header-title",
        )
        yield Static(
            "  [bold]F[/]ile        [bold]H[/]elp        ",
            id="header-menubar",
        )
        yield Static(self._build_body_markup(), id="header-body")

    def _build_body_markup(self) -> str:
        name = escape(self._human_name) if self._human_name else "…"
        gid = escape(self._group_id)
        kpref = escape(self._our_key[:12])
        peer_bits = [escape(m[:10]) + "…" for m in self._member_keys[:6]]
        peers_s = " · ".join(peer_bits)
        if len(self._member_keys) > 6:
            peers_s += f" [dim](+{len(self._member_keys) - 6})[/]"

        parts: list[str] = [
            f"[#3A5068]{kpref}… · API :{self._port}[/]\n",
        ]
        if self._session_ready and self._recv_mode:
            rm = escape(self._recv_mode)
            parts.append(
                f"[#1E3A50]#[/][#3D6488]{gid}[/] · [bold #1E3A50]{name}[/] · "
                f"{self._member_count} members · recv [bold #4A6FA0]{rm}[/] · "
                f"[#3A5068]Yggdrasil E2E[/]\n"
            )
        if peers_s:
            parts.append(f"[bold #1E3A50]Peers[/]  [#2D4A62]{peers_s}[/]\n")
        if self._openclaw_agent:
            ea = escape(self._openclaw_agent)
            parts.append(
                f"[bold #1E3A50]Agent[/]  [#2D4A62]{ea}[/]  [#3A5068](@[/][#2D4A62]{ea}[/][#3A5068])[/]\n"
            )
        if not self._session_ready:
            parts.append("[#6B8AA8 italic]Starting session…[/]\n")
        return "".join(parts)

    def _refresh_body(self) -> None:
        self.query_one("#header-body", Static).update(self._build_body_markup())

    def set_name(self, name: str) -> None:
        self._human_name = name
        self._refresh_body()

    def set_openclaw_agent(self, agent_name: str | None) -> None:
        self._openclaw_agent = agent_name
        self._refresh_body()

    def set_session(self, recv_mode: str) -> None:
        self._session_ready = True
        self._recv_mode = recv_mode
        self._refresh_body()


class ChatLog(VerticalScroll):
    """Scrollable message history."""

    DEFAULT_CSS = """
    ChatLog {
        height: 1fr;
        width: 1fr;
        padding: 1 2;
        margin: 0 2;
        background: $surface;
        color: $foreground;
    }
    """


class MessageBubble(Vertical):
    """One chat message: meta line + body (Markdown for agents, plain for humans)."""

    DEFAULT_CSS = """
    MessageBubble {
        width: 100%;
        max-width: 100%;
        height: auto;
        min-height: 1;
        background: $panel;
    }
    MessageBubble.outgoing {
        margin: 0 0 1 0;
        padding: 0 1 0 0;
        border-left: none;
        border-right: solid $accent;
    }
    MessageBubble.outgoing .msg-meta,
    MessageBubble.outgoing .msg-body {
        text-align: right;
    }
    MessageBubble.incoming {
        margin: 0 0 1 0;
        padding: 0 0 0 1;
        border-left: solid $secondary;
    }
    MessageBubble .msg-meta {
        margin-bottom: 0;
        text-style: bold;
        color: $foreground;
    }
    MessageBubble .msg-body {
        margin-top: 0;
        color: $foreground;
    }
    """


class SystemNote(Static):
    """Centered status message."""

    DEFAULT_CSS = """
    SystemNote {
        text-align: center;
        text-style: italic dim;
        color: $foreground;
        margin: 0 0 1 0;
    }
    """


# ── App ────────────────────────────────────────────────────────


class GroupChatApp(App):
    """AXL Group Chat — encrypted multi-party messaging."""

    TITLE = "AXL Group Chat"
    ENABLE_COMMAND_PALETTE = True

    CSS = """
    Screen {
        background: $background;
    }

    #main-stack {
        layout: vertical;
        height: 1fr;
        width: 1fr;
    }

    #chat-input {
        margin: 0 2 0 2;
        width: 1fr;
    }

    #chat-input:focus {
        border: solid $primary;
    }

    #chat-input:disabled {
        background: $panel;
        text-style: dim;
        border: solid $border-blurred;
    }
    """

    BINDINGS = [
        Binding("ctrl+q", "quit", "Quit", show=True, priority=True),
        Binding("escape", "quit", "Quit", show=False),
        Binding("ctrl+p", "command_palette", "palette", show=True),
        Binding("ctrl+t", "change_theme", "Theme", show=True),
        Binding("f1", "show_help_panel", "Keys", show=True),
    ]

    def get_system_commands(self, screen: Screen) -> Iterable[SystemCommand]:
        seen_theme = False
        for cmd in super().get_system_commands(screen):
            if getattr(cmd, "title", None) == "Theme":
                seen_theme = True
            yield cmd
        if not seen_theme:
            yield SystemCommand(
                "Theme",
                "Change the application theme",
                self.action_change_theme,
            )

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
        try:
            from axl_theme_packs import AXL_THEMES, DEFAULT_AXL_THEME_NAME

            for theme in AXL_THEMES:
                self.register_theme(theme)
            self.theme = DEFAULT_AXL_THEME_NAME
        except ImportError:
            pass

    # ── layout ─────────────────────────────────────────────

    def compose(self) -> ComposeResult:
        total = len(self.members) + 1
        placeholder = self.our_name or "…"
        header_agent = self.preset_agent_name if self.openclaw_mode else None
        yield HeaderPanel(
            self.our_key,
            placeholder,
            self.group_id,
            total,
            self.port,
            self.members,
            openclaw_agent=header_agent,
        )
        with Vertical(id="main-stack"):
            yield ChatLog()
            yield AxlInput(placeholder="Type a message…", id="chat-input", disabled=True)

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
        recv_mode = "dispatcher" if self.dispatcher_url else "direct"
        self.query_one(HeaderPanel).set_session(recv_mode)
        chat_input = self.query_one("#chat-input", AxlInput)
        chat_input.disabled = False
        chat_input.focus()
        self._start_recv()

    # ── message rendering ──────────────────────────────────

    def _scroll_to_bottom(self) -> None:
        self.query_one(ChatLog).scroll_end(animate=False)

    def _out(self, text: str) -> None:
        ts = datetime.now().strftime("%H:%M:%S")
        meta_line = f"▶ {escape(self.our_name)} (you)  {ts}"
        body = Static(escape(text), classes="msg-body", markup=False)
        bubble = MessageBubble(
            Static(meta_line, classes="msg-meta", markup=False),
            body,
            classes="outgoing",
        )
        self.query_one(ChatLog).mount(bubble)
        self.set_timer(0.05, self._scroll_to_bottom)

    def _in(
        self,
        text: str,
        sender_name: str,
        sender_key: str,
        from_agent: bool = False,
    ) -> None:
        ts = datetime.now().strftime("%H:%M:%S")
        short = sender_key[:6]
        display = sender_name if sender_name else short
        role_suffix = " (agent)" if from_agent else ""
        meta_line = f"◀ {escape(display)}{role_suffix}  {short} · {ts}"
        if from_agent:
            body = Static(Markdown(text), classes="msg-body")
            bubble_cls = "incoming agent"
        else:
            body = Static(escape(text), classes="msg-body", markup=False)
            bubble_cls = "incoming"
        bubble = MessageBubble(
            Static(meta_line, classes="msg-meta", markup=False),
            body,
            classes=bubble_cls,
        )
        self.query_one(ChatLog).mount(bubble)
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
                    from_agent = bool(msg.get("_from_agent"))
                    self.call_from_thread(
                        self._in, text, sender_name, sender_key, from_agent,
                    )
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
