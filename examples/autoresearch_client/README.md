# autoresearch_client

P2P findings-sharing for collaborative autoresearch agents on the Yggdrasil mesh network.

Multiple agents — each running the autoresearch loop independently on their own GPU — share
experiment results over the network in real time.  When one agent finds a better `train.py`,
peers can adopt it as their new baseline and keep improving from there.  The agents converge
faster than any single agent could alone.

## How it works

After each experiment round an agent broadcasts its result (metric + winning `train.py` source)
to every reachable node.  Before each round it drains the receive queue and checks whether any
peer has found something significantly better.  If so, it adopts their code, validates it by
actually running it, and continues from that new baseline.

Peer discovery is automatic: `GET /topology` returns the full Yggdrasil spanning tree, so every
node in the network is reachable regardless of how many hops away it is.

## Files

| File | Purpose |
|------|---------|
| `research_network.py` | Core library + shell CLI.  Zero external dependencies (stdlib only). |
| `agent_loop.py` | Reference scaffolding showing how to integrate the library into the loop. |
| `program.md` | Patched autoresearch spec with network sharing built into the loop. |

## Prerequisites

1. **Yggdrasil node running** on `127.0.0.1:9002` (the default).
   ```bash
   cd yggdrasil-node
   go build -o node ./cmd/node/
   ./node -config node-config.json
   ```
   The included `node-config.json` connects to public bootstrap peers — no extra coordination
   needed between participants.

2. **Autoresearch repo** set up per its own README (data cache populated, `uv` environment ready).

## Quickstart

### Check you are connected

```bash
python research_network.py status
```

```
our_id:  3f8a2c1d...
our_ipv6:200:abcd::1
peers:   3
  4a7b9e2f...
  1c3d5e7a...
  8b2f4c6d...
```

### Run the collaborative loop

Copy `program.md` from this directory into your autoresearch repo (or fork), then start a Claude
Code session there:

```
Run program.md
```

Claude will handle the rest autonomously: experiment, share, receive, adopt, repeat.

### Manual broadcast (useful for testing)

```bash
python research_network.py broadcast \
    --round 1 --val-bpb 0.9979 --memory 44.0 \
    --status keep --commit a1b2c3d \
    --description "baseline" \
    --train-py /path/to/autoresearch/train.py
```

### Manual receive

```bash
python research_network.py recv
```

Prints one JSON line per new finding (train.py source omitted from stdout).

## Library API

```python
from research_network import ResearchNetwork

net = ResearchNetwork()                        # default: http://127.0.0.1:9002
net.drain_recv_queue()                         # collect peer findings, update registry
best = net.best_peer_finding()                 # lowest val_bpb seen from any peer
if best and net.should_adopt(best, my_bpb):    # True if gap >= 0.002 bpb
    best.write_train_py("train.py")            # overwrite with peer's source

net.broadcast_finding(
    round_num=7,
    val_bpb=0.9821,
    memory_gb=44.2,
    status="keep",           # "keep" | "discard" | "crash"
    description="cosine LR decay",
    commit="a1b2c3d",
    train_py_path="train.py",  # only included in message when status="keep"
)
```

## Message format

All messages are JSON bytes over `/send`.  The `train_py` field is only populated for `keep`
results to keep bandwidth reasonable (~20–50 KB per kept round).

```json
{
  "proto":       1,
  "type":        "finding",
  "round":       7,
  "val_bpb":     0.9821,
  "memory_gb":   44.2,
  "status":      "keep",
  "description": "cosine LR decay",
  "commit":      "a1b2c3d",
  "timestamp":   1711000000.0,
  "sender_id":   "3f8a2c1d...",
  "train_py":    "import torch\n..."
}
```

## Multi-agent convergence

With N agents each running a 5-minute experiment loop:
- Each agent explores ~12 ideas per hour independently
- Every `keep` result is immediately available to all peers
- An agent that hits a dead end can pivot to a peer's better baseline and explore from there
- The effective search rate is super-linear: agents don't just parallelize, they share signal

The adoption threshold (`min_improvement=0.002`) prevents agents from thrashing on noise while
still allowing meaningful leaps to a peer's superior configuration.

## Configuration

| Parameter | Default | Notes |
|-----------|---------|-------|
| `api_url` | `http://127.0.0.1:9002` | Local node HTTP API |
| `min_improvement` | `0.002` | Min val_bpb gap to adopt a peer's train.py |
| `ADOPT_THRESHOLD` in `agent_loop.py` | `0.002` | Same, for the reference loop |

---

## Appendix: Design Rationale

### Why `/send` instead of `/mcp` or `/a2a`

The node offers three communication patterns: fire-and-forget (`/send`), synchronous RPC
(`/mcp`), and agent-to-agent RPC (`/a2a`).  Findings sharing uses `/send` for one reason:
the agents are always busy.  During an experiment the agent is blocked waiting for a 5-minute
training run.  A synchronous call to a busy peer would time out or stall the sender.
Fire-and-forget means a broadcast takes milliseconds regardless of what peers are doing, and a
peer that is mid-run simply picks up the messages the next time it polls `/recv`.  The
asynchrony is a feature, not a limitation.

### Why broadcast to the full spanning tree, not just direct peers

`/topology` returns two lists: `peers[]` (direct TCP connections) and `tree[]` (every node
Yggdrasil knows about).  Yggdrasil routes `/send` to any node in the tree, not just direct
neighbours.  If we only sent to direct peers, a finding would need multiple agent-loop
iterations to propagate across the network — a 10-node network with 3 direct peers each could
take 3–4 rounds before every agent sees a result.  Sending directly to all tree nodes collapses
that to one round.  The bandwidth cost is linear in network size but tiny in absolute terms:
10 peers × 50 KB = 500 KB per broadcast, comfortably under the 16 MB message limit and
negligible compared to the 5-minute experiment it follows.

### Why JSON instead of msgpack

The existing tensor-sharing example in `client.py` uses msgpack because it is efficient for
binary data like PyTorch tensors.  Here the payload is almost entirely text (`train.py` source
plus a handful of scalar fields), so JSON is equally compact and gains two things: it is
human-readable (you can inspect messages with any tool), and it requires no dependencies —
`json` is Python stdlib, which matters because `research_network.py` is designed to be dropped
into any Python environment without an install step.

### Why share the full `train.py` source

Sharing the metric alone tells a peer *that* something worked.  It does not tell them *what*
the winning configuration was.  The `description` field is human-written and lossy — "cosine
LR with warmup" could mean a dozen different implementations.  With the actual source, adoption
is a single file write followed by a validation run.  The peer does not have to guess or
reconstruct — they get the exact code that produced the result.  A typical `train.py` is
10–50 KB, which is three orders of magnitude below the 16 MB message ceiling.  The cost of
including it is negligible; the cost of omitting it is that the collaboration degrades to
sharing gossip instead of sharing knowledge.

### Why `train_py` is only included for `keep` results

A discarded or crashed experiment means the agent has already reverted `train.py` to its
previous state.  There is nothing to share: the code at HEAD after a revert is the same code
that was already broadcast in the prior `keep` round.  Sending it again would double the
message size for every failed experiment while adding no new information.  The rule is simple:
if `status == "keep"`, the agent attaches the source; otherwise it sends metrics only.  Peers
see the full picture either way — they know what was tried and that it did not work — but only
pay the bandwidth cost when there is something worth adopting.

### Why the 0.002 adoption threshold

The threshold guards against two failure modes.  Too low (< 0.001) and agents thrash:
run-to-run noise on the same config can exceed 0.001 bpb, so agents would constantly adopt
each other's code without making real progress, and the branch history would fill with
`adopt:` commits that don't actually represent improvements.  Too high (> 0.005) and agents
ignore genuine peer discoveries, defeating the purpose of collaboration.  0.002 sits at
roughly twice the observed noise floor, making it a meaningful signal threshold: if a peer
beats you by 0.002 or more, that is almost certainly a real improvement worth validating,
not measurement variance.  Critically, this threshold only controls adoption decisions.
Every result — keep, discard, crash — is still broadcast unconditionally, so peers always
have a complete picture of what has been explored.

### Why drain the queue before choosing an experiment, not after

The loop drains `/recv` at the start of each round, before deciding what to try.  The
alternative — draining after broadcasting — has a race condition: a peer's result from the
*current* round would arrive in the queue while our experiment is running, but we wouldn't
see it until the round *after* next.  Front-loading the drain means the agent acts on the
freshest available information when making its decision.  Since experiments take ~5 minutes,
the queue accumulates real signal between rounds; draining first maximises the chance that a
superior peer baseline is visible before we commit to a direction.

### Why the transport-layer sender ID overrides the payload field

The `X-From-Peer-Id` header is set by the local Yggdrasil node from the cryptographic public
key of the inbound connection — the agent's code has no say in it.  The `sender_id` field
inside the JSON payload is self-reported by the sending agent.  In the normal case they
agree, but the header is more reliable: a freshly-started agent might not yet know its own
public key and could send an empty or stale `sender_id`.  Using the header as the authoritative
source means deduplication and the per-peer registry are always keyed on the real network
identity, not whatever the agent happens to write in the payload.
