# AXL Group Chat — Quickstart

Encrypted P2P group chat with an optional AI agent (OpenClaw).
Two terminals. Five minutes.

---

## 1. Build the node

```bash
git clone https://github.com/gensyn-ai/axl.git axl
cd axl
go build -o node ./cmd/node/
```

> Go 1.26+? Prefix with `GOTOOLCHAIN=go1.25.5 go build -o node ./cmd/node/`

## 2. Generate a key and install Python deps

```bash
openssl genpkey -algorithm ed25519 -out private.pem
pip install -r examples/group-chat/requirements.txt
```

The key gives your node a persistent identity (same public key across restarts). The pip install pulls in `textual` and `requests` — nothing heavy.

## 3. Start the node

```bash
./node -config node-config.json
```

Leave this running (**Terminal 1**). The included config peers with the bootstrap nodes automatically — you'll see your public key in the output.

## 4. Get your public key

Your node prints it on startup. Or run:

```bash
curl -s http://127.0.0.1:9002/topology | python3 -c "import sys,json; print(json.load(sys.stdin)['our_public_key'])"
```

**Send this key to the people you want to chat with, and get theirs.** This is how AXL knows who to send messages to. Keys look like: `1ee862344fb283395143ac9775150d2e5936efd6e78ed0db83e3f290d3d539ef`

## 5. Join the chat

Open a second terminal (**Terminal 2**):

```bash
cd axl/examples/group-chat
python3 group_chat.py --port 9002 --group alpha --members THEIR_KEY
```

Replace `THEIR_KEY` with the other person's public key. For multiple people, comma-separate them:

```bash
python3 group_chat.py --port 9002 --group alpha --members KEY1,KEY2,KEY3
```

That's it. You're in.

> **Note:** `--auto` works for local testing (nodes on the same machine), but for chatting with someone on a different machine, always use `--members` with their public key.

### With OpenClaw (AI agent)

Add `--openclaw` and your gateway token to have your AI agent join the chat:

```bash
python3 group_chat.py --port 9002 --group alpha \
    --members THEIR_KEY \
    --openclaw --gateway-token YOUR_TOKEN
```

**Naming in the TUI:** With `--openclaw`, you are prompted for **your display name**, then **your agent’s name** (second screen). There is no `--agent-name` or `OPENCLAW_DISPLAY_NAME` shortcut in `group_chat.py` — the agent label is always chosen in the UI.

**Mentions:** By default the agent **only sends a reply into the chat** when a message contains **`@YourAgentName`** exactly (same spelling and capitalization as you typed in the TUI, e.g. `@JdubsBot` not `@jdubsbot`). Use `--openclaw-respond-all` or `OPENCLAW_RESPOND_ALL=1` if you want it to answer **every** message without an `@`.

**Transcript (context):** The bridge **records** recent group messages in memory. When someone `@`-mentions your agent, **one** request is sent to OpenClaw that includes a **rolling transcript** (default last **80** lines) plus the new instruction — so the model can use earlier lines that never triggered a call by themselves (e.g. “blue red green” before “`@agent` what colors?”). Increase or shrink the window with `OPENCLAW_CONTEXT_MAX_MESSAGES` (5–500). Transcript **resets** when you restart `group_chat.py`.

**Standalone bridge:** If you run `openclaw_bridge.py` by itself (not the unified launcher), agent naming uses `--name` / `OPENCLAW_DISPLAY_NAME` as documented for that script.

---

## OpenClaw one-time setup

Skip this if you don't want an AI agent.

### Prerequisites

- **Node.js 22+** — [nodejs.org](https://nodejs.org/)

### Steps

**1. Install and onboard:**

```bash
npm install -g openclaw@latest
openclaw onboard --install-daemon
```

Follow the prompts — it asks for your AI provider API key (Anthropic, OpenAI, etc.).

**2. Enable the HTTP API.** Open `~/.openclaw/openclaw.json` and add this block:

```json5
{
  gateway: {
    http: {
      endpoints: {
        chatCompletions: { enabled: true },
      },
    },
  },
}
```

If the file already has content, merge this into the existing structure — don't replace the whole file.

**3. Restart and get your token:**

```bash
openclaw restart
openclaw token
```

Copy the token. To avoid retyping it every launch, add this to your `~/.zshrc`:

```bash
export OPENCLAW_GATEWAY_TOKEN=your_token_here
```

Then just `--openclaw` works without `--gateway-token`.

---

## TL;DR

| Terminal | Command |
|----------|---------|
| 1 | `./node -config node-config.json` |
| 2 | `python3 group_chat.py --port 9002 --group alpha --members THEIR_KEY` |

Add `--openclaw` to bring your AI agent into the chat. Drop it for human-only.

With OpenClaw: you name the agent in the TUI; replies default to **`@AgentName` only**; each reply request can include up to **`OPENCLAW_CONTEXT_MAX_MESSAGES`** (default `80`) lines of prior chat as context.

---

## Reference

### How peering works

The `node-config.json` has bootstrap node addresses in `Peers`. Everyone points at the same bootstraps — once on the mesh, AXL routes by public key. No direct connections, no port forwarding.

### All flags

**Core:**

| Flag | Default | What it does |
|------|---------|-------------|
| `--port` | `9002` | AXL node API port |
| `--name` | *(prompted)* | Your display name |
| `--group` | `general` | Group ID (must match for all participants) |
| `--auto` | — | Auto-discover peers |
| `--members` | — | Manual peer keys (comma-separated) |

**OpenClaw:**

| Flag | Env Var | Default |
|------|---------|---------|
| `--openclaw` | — | off |
| `--gateway-token` | `OPENCLAW_GATEWAY_TOKEN` | *(required)* |
| `--gateway` | `OPENCLAW_GATEWAY_URL` | `http://127.0.0.1:18789` |
| `--model` | `OPENCLAW_MODEL` | `openclaw/default` |
| `--system-prompt` | `OPENCLAW_SYSTEM_PROMPT` | *(none)* |
| `--openclaw-respond-all` | `OPENCLAW_RESPOND_ALL` | off; agent only replies when the message contains `@AGENT_NAME` exactly |
| *(transcript depth)* | `OPENCLAW_CONTEXT_MAX_MESSAGES` | `80` (clamped 5–500) — how many recent lines are included in the prompt when an @mention fires |

**`group_chat.py` agent name:** Always the **second TUI prompt** after your display name (no CLI/env override).

**What gets sent to OpenClaw (default mode):** The HTTP API is called **only** when a message contains your `@AgentName` (or on every message if `--openclaw-respond-all`). Each such call includes the **in-memory transcript** so the model sees prior messages as context. Messages without `@` are **not** sent to the API by themselves — they only appear inside that transcript on a later @mention. The `@` token is stripped from the text sent to the gateway where helpful, to avoid confusing OpenClaw’s session handling.

### Files in this folder

| File | What |
|------|------|
| `group_chat.py` | **Run this.** Everything else is imported automatically. |
| `group_chat_tui.py` | Chat UI |
| `dispatcher.py` | Message fan-out (multi-consumer support) |
| `openclaw_bridge.py` | AI agent bridge |
| `agent_inbox.py` | Debug utility |

### Troubleshooting

| Error | Fix |
|-------|-----|
| "Cannot reach AXL node" | Node isn't running or wrong `--port` |
| "No connected peers found" | Wait a few seconds after node startup, or check `Peers` in config |
| "Cannot reach OpenClaw gateway" | Run `openclaw start` |
| "401 (Unauthorized)" | Pass `--gateway-token` (find it with `openclaw token`) |
| "gateway returned 4xx" | Enable `chatCompletions` in `~/.openclaw/openclaw.json` |
| "address already in use" | Kill the old process: `lsof -ti :9002 \| xargs kill` |
| "urllib3 NotOpenSSLWarning" | Harmless on macOS, ignore it |
| Messages not appearing | Everyone must use the same `--group` value |
| Agent ignores earlier lines when @mentioned | Restart `group_chat.py` only clears the transcript; ensure those lines arrived after the bridge started. Raise `OPENCLAW_CONTEXT_MAX_MESSAGES` if the chat is very long |
| Agent says “no session” / odd disclaimers | Usually fixed in current bridge prompts; update `group_chat.py` / `openclaw_bridge.py` from the repo if you’re on an old copy |
