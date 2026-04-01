"""
GossipSub implementation over the Yggdrasil transport layer.

Optimized variant using lazy-first forwarding: on receiving a new message,
eagerly forward to 1 mesh peer and send IHAVE to the rest.  Peers that
don't receive the eager copy fetch it via IWANT, with deduplication to
avoid redundant requests.  This achieves near-optimal redundancy (~1.0)
while maintaining fast delivery.

The GossipSub class is instantiated with:
  - config:   GossipConfig instance
  - node_id:  64-char hex string (peer identity)
  - send_fn:  callable(dest_key: str, data: bytes) -> None
  - recv_fn:  callable() -> tuple[str, bytes] | None  (returns None if queue empty)

send_fn / recv_fn map to POST /send and GET /recv on the yggdrasil HTTP
bridge (127.0.0.1:9002).
"""

from __future__ import annotations

import base64
import json
import random
import time
from dataclasses import dataclass
from typing import Callable


# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------

@dataclass
class GossipConfig:
    D: int = 3                       # target mesh degree
    D_low: int = 2                   # min acceptable mesh degree (triggers graft)
    D_high: int = 4                  # max acceptable mesh degree (triggers prune)
    D_gossip: int = 1                # non-mesh peers to send IHAVE to per heartbeat
    heartbeat_interval: float = 1.0  # seconds between heartbeats
    max_ihave_length: int = 5000     # max message IDs per IHAVE emission


# ---------------------------------------------------------------------------
# GossipSub
# ---------------------------------------------------------------------------

class GossipSub:
    """
    GossipSub with lazy-first forwarding.

    Mesh overlay:
      Each subscribed topic has a mesh — a subset of connected peers.
      On publish, messages are eagerly pushed to all mesh peers.
      On forward (received from another node), messages are eagerly pushed
      to 1 mesh peer and lazily announced (IHAVE) to the rest.  Peers
      that need the message request it via IWANT.

    IWANT deduplication:
      Each message is only requested via IWANT once, preventing redundant
      fetches when multiple peers announce the same message.

    Mesh maintenance (heartbeat-driven):
      - Under D_low peers  -> GRAFT random candidates until D is reached.
      - Over D_high peers  -> PRUNE excess peers back to D.

    Control messages (all serialised as JSON over raw bytes):
      MESSAGE  — a pub/sub payload; forwarded by mesh members
      GRAFT    — "add me to your mesh for <topic>"
      PRUNE    — "remove me from your mesh for <topic>"
      IHAVE    — "I have seen these message IDs"
      IWANT    — "please send me these messages"
    """

    def __init__(self, config: GossipConfig, node_id: str,
                 send_fn: Callable, recv_fn: Callable) -> None:
        self.config = config
        self.node_id = node_id
        self.send_fn = send_fn
        self.recv_fn = recv_fn

        self.peers: set[str] = set()         # all transport-reachable peers
        self.mesh: dict[str, set[str]] = {}  # topic -> set of mesh peer ids
        self.subscriptions: set[str] = set()

        self.seen_msgs: set[str] = set()     # dedup set
        self.msg_cache: dict[str, dict] = {} # msg_id -> full message dict
        self._pending_iwant: set[str] = set()  # msg_ids already requested via IWANT

        self._msg_counter: int = 0
        self._last_heartbeat: float = time.time()

        # Stats
        self._published: list[str] = []       # msg_ids published by this node
        self._received: set[str] = set()      # unique msg_ids received from network
        self._total_received: int = 0         # total deliveries including duplicates
        self._hop_counts: dict[str, int] = {} # msg_id -> hop count at first delivery

    # ------------------------------------------------------------------
    # Public interface (do not change signatures)
    # ------------------------------------------------------------------

    def add_peer(self, peer_id: str) -> None:
        """Register a directly reachable peer."""
        self.peers.add(peer_id)

    def subscribe(self, topic: str) -> None:
        """Subscribe to a topic and initialise an empty mesh for it."""
        self.subscriptions.add(topic)
        self.mesh.setdefault(topic, set())

    def publish(self, topic: str, data: bytes) -> str:
        """Publish a message on a topic. Returns the generated msg_id."""
        msg_id = self._gen_msg_id()
        self._published.append(msg_id)
        self.seen_msgs.add(msg_id)

        msg = {
            'type': 'gossipsub',
            'msg_type': 'MESSAGE',
            'topic': topic,
            'msg_id': msg_id,
            'origin': self.node_id,
            'from': self.node_id,
            'hop': 0,
            'data': base64.b64encode(data).decode(),
        }
        self.msg_cache[msg_id] = msg

        # Publisher sends eagerly to all mesh peers for fast initial fanout
        targets = self.mesh.get(topic, set()) or self.peers
        for peer in list(targets):
            self._send(peer, msg)

        return msg_id

    def tick(self) -> None:
        """
        Called repeatedly by the simulation loop.
        Drains all pending inbound messages, then runs heartbeat logic when due.
        """
        while True:
            result = self.recv_fn()
            if result is None:
                break
            from_id, raw = result
            self._handle_raw(from_id, raw)

        now = time.time()
        if now - self._last_heartbeat >= self.config.heartbeat_interval:
            self._heartbeat()
            self._last_heartbeat = now

    def get_stats(self) -> dict:
        """Return serialisable stats for the simulation harness."""
        return {
            'node_id': self.node_id,
            'published_msg_ids': list(self._published),
            'received_msg_ids': list(self._received),
            'total_received': self._total_received,
            'hop_counts': dict(self._hop_counts),
        }

    # ------------------------------------------------------------------
    # Message handlers
    # ------------------------------------------------------------------

    def _handle_raw(self, from_id: str, raw: bytes) -> None:
        try:
            msg = json.loads(raw)
        except Exception:
            return
        if msg.get('type') != 'gossipsub':
            return
        mt = msg.get('msg_type')
        if mt == 'MESSAGE':
            self._handle_message(from_id, msg)
        elif mt == 'GRAFT':
            self._handle_graft(from_id, msg)
        elif mt == 'PRUNE':
            self._handle_prune(from_id, msg)
        elif mt == 'IHAVE':
            self._handle_ihave(from_id, msg)
        elif mt == 'IWANT':
            self._handle_iwant(from_id, msg)

    def _handle_message(self, from_id: str, msg: dict) -> None:
        msg_id = msg.get('msg_id', '')
        topic = msg.get('topic', '')
        hop = msg.get('hop', 0)

        self._total_received += 1

        if msg_id in self.seen_msgs:
            return  # duplicate — count but do not forward

        self.seen_msgs.add(msg_id)
        self.msg_cache[msg_id] = msg
        self._received.add(msg_id)
        self._hop_counts[msg_id] = hop

        if topic not in self.subscriptions:
            return

        fwd = dict(msg)
        fwd['hop'] = hop + 1
        fwd['from'] = self.node_id

        # Lazy-first forwarding: eager push to 1 peer, IHAVE to the rest.
        # Skips the sender and message origin to avoid wasted sends.
        origin = msg.get('origin', '')
        candidates = [p for p in self.mesh.get(topic, set()) if p != from_id and p != origin]
        random.shuffle(candidates)

        if candidates:
            self._send(candidates[0], fwd)
        if len(candidates) > 1:
            self._send_ihave(candidates[1:], topic, [msg_id])

    def _handle_graft(self, from_id: str, msg: dict) -> None:
        topic = msg.get('topic', '')
        if topic not in self.subscriptions:
            return
        mesh = self.mesh.setdefault(topic, set())
        if len(mesh) < self.config.D_high:
            mesh.add(from_id)
        else:
            self._send(from_id, {
                'type': 'gossipsub', 'msg_type': 'PRUNE',
                'topic': topic, 'peers': [],
            })

    def _handle_prune(self, from_id: str, msg: dict) -> None:
        topic = msg.get('topic', '')
        self.mesh.get(topic, set()).discard(from_id)

    def _handle_ihave(self, from_id: str, msg: dict) -> None:
        topic = msg.get('topic', '')
        if topic not in self.subscriptions:
            return
        wanted = [mid for mid in msg.get('msg_ids', [])
                  if mid not in self.seen_msgs and mid not in self._pending_iwant]
        if wanted:
            self._pending_iwant.update(wanted)
            self._send(from_id, {
                'type': 'gossipsub', 'msg_type': 'IWANT',
                'msg_ids': wanted[:64],
            })

    def _handle_iwant(self, from_id: str, msg: dict) -> None:
        for mid in msg.get('msg_ids', []):
            if mid in self.msg_cache:
                cached = dict(self.msg_cache[mid])
                cached['from'] = self.node_id
                self._send(from_id, cached)

    # ------------------------------------------------------------------
    # Heartbeat
    # ------------------------------------------------------------------

    def _heartbeat(self) -> None:
        for topic in list(self.subscriptions):
            self._maintain_mesh(topic)
            self._emit_gossip(topic)

    def _maintain_mesh(self, topic: str) -> None:
        """Keep mesh size between D_low and D_high by grafting / pruning."""
        mesh = self.mesh.setdefault(topic, set())
        mesh &= self.peers  # remove any peers that disappeared

        if len(mesh) < self.config.D_low:
            candidates = list(self.peers - mesh)
            random.shuffle(candidates)
            want = self.config.D - len(mesh)
            for peer in candidates[:want]:
                mesh.add(peer)
                self._send(peer, {
                    'type': 'gossipsub', 'msg_type': 'GRAFT', 'topic': topic,
                })

        elif len(mesh) > self.config.D_high:
            excess = list(mesh)
            random.shuffle(excess)
            for peer in excess[:len(mesh) - self.config.D]:
                mesh.discard(peer)
                self._send(peer, {
                    'type': 'gossipsub', 'msg_type': 'PRUNE',
                    'topic': topic, 'peers': [],
                })

    def _emit_gossip(self, topic: str) -> None:
        """Send IHAVE to a random sample of non-mesh peers (lazy pull trigger)."""
        recent = list(self.seen_msgs)[-self.config.max_ihave_length:]
        if not recent:
            return

        non_mesh = list(self.peers - self.mesh.get(topic, set()))
        if not non_mesh:
            return

        num = min(self.config.D_gossip, len(non_mesh))
        for peer in random.sample(non_mesh, num):
            self._send(peer, {
                'type': 'gossipsub', 'msg_type': 'IHAVE',
                'topic': topic, 'msg_ids': recent,
            })

    def _send_ihave(self, peers: list[str], topic: str, msg_ids: list[str]) -> None:
        """Send IHAVE to specific peers."""
        for peer in peers:
            self._send(peer, {
                'type': 'gossipsub', 'msg_type': 'IHAVE',
                'topic': topic, 'msg_ids': msg_ids,
            })

    # ------------------------------------------------------------------
    # Helpers
    # ------------------------------------------------------------------

    def _gen_msg_id(self) -> str:
        self._msg_counter += 1
        return f"{self.node_id[:8]}:{self._msg_counter:06d}"

    def _send(self, peer_id: str, msg: dict) -> None:
        try:
            self.send_fn(peer_id, json.dumps(msg).encode())
        except Exception:
            pass
