"""
ResearchNetwork: P2P findings-sharing for autoresearch agents on the Yggdrasil mesh.

Zero external dependencies — uses only Python stdlib.

Library usage (inside your autoresearch loop)
---------------------------------------------
    from research_network import ResearchNetwork

    net = ResearchNetwork()   # connects to local node at http://127.0.0.1:9002

    # After each experiment round:
    net.broadcast_finding(
        round_num=7,
        val_bpb=0.9821,
        memory_gb=44.2,
        status="keep",
        description="cosine LR decay with 50-step warmup",
        commit="a1b2c3d",
        train_py_path="train.py",   # include source only for "keep" results
    )

    # Before the next round, collect what peers have found:
    net.drain_recv_queue()
    best = net.best_peer_finding()
    if best and net.should_adopt(best, my_best_bpb):
        print(f"Adopting peer train.py — their bpb: {best.val_bpb:.6f}")
        best.write_train_py("train.py")

CLI usage (from shell / bash scripts)
--------------------------------------
    # Show topology and peer summary
    python research_network.py status

    # Drain recv queue and print new findings (JSON, one per line)
    python research_network.py recv

    # Broadcast a finding after a round
    python research_network.py broadcast \\
        --round 7 --val-bpb 0.9821 --memory 44.2 \\
        --status keep --commit a1b2c3d \\
        --description "cosine LR decay" \\
        --train-py train.py

Protocol
--------
  Wire format : JSON-encoded bytes, sent via POST /send (Content-Type: application/octet-stream)
  Peer discovery: GET /topology — combines peers[] (direct) and tree[] (full spanning tree)
                  so we reach every node in the network via Yggdrasil routing.
  Message schema (v1):
    {
      "proto":       1,
      "type":        "finding",
      "round":       <int>,
      "val_bpb":     <float>,       -- 0.0 on crash
      "memory_gb":   <float>,       -- 0.0 on crash
      "status":      "keep"|"discard"|"crash",
      "description": <str>,         -- no tabs or commas
      "commit":      <str>,         -- 7-char git hash
      "timestamp":   <float>,       -- unix epoch
      "sender_id":   <str>,         -- sender's Yggdrasil public key (64-char hex)
      "train_py":    <str|null>     -- full train.py source, only present for "keep"
    }
"""

from __future__ import annotations

import json
import logging
import time
import urllib.error
import urllib.request
from dataclasses import dataclass
from typing import Optional

PROTO_VERSION = 1
MSG_TYPE_FINDING = "finding"

log = logging.getLogger(__name__)


@dataclass
class Finding:
    """A single experiment result, either our own or received from a peer."""

    proto: int
    round_num: int
    val_bpb: float
    memory_gb: float
    status: str           # "keep" | "discard" | "crash"
    description: str
    commit: str
    timestamp: float
    sender_id: str        # Yggdrasil public key (64-char hex)
    train_py: Optional[str] = None  # full train.py source (only for "keep")

    @property
    def improved(self) -> bool:
        return self.status == "keep"

    def write_train_py(self, path: str = "train.py") -> None:
        """Write the peer's train.py to disk. Only valid when train_py is not None."""
        if self.train_py is None:
            raise ValueError(
                f"Finding from {self.sender_id[:12]}... has no train.py "
                f"(status={self.status})"
            )
        with open(path, "w") as f:
            f.write(self.train_py)
        log.info(f"Wrote peer train.py ({len(self.train_py):,} bytes) to {path}")

    def to_dict(self) -> dict:
        return {
            "proto": self.proto,
            "type": MSG_TYPE_FINDING,
            "round": self.round_num,
            "val_bpb": self.val_bpb,
            "memory_gb": self.memory_gb,
            "status": self.status,
            "description": self.description,
            "commit": self.commit,
            "timestamp": self.timestamp,
            "sender_id": self.sender_id,
            "train_py": self.train_py,
        }

    @classmethod
    def from_dict(cls, d: dict) -> "Finding":
        return cls(
            proto=d.get("proto", 1),
            round_num=int(d["round"]),
            val_bpb=float(d["val_bpb"]),
            memory_gb=float(d["memory_gb"]),
            status=str(d["status"]),
            description=str(d["description"]),
            commit=str(d["commit"]),
            timestamp=float(d["timestamp"]),
            sender_id=str(d["sender_id"]),
            train_py=d.get("train_py"),
        )


# ── Low-level HTTP helpers (stdlib only) ──────────────────────────────────


def _get(url: str, timeout: int = 10) -> tuple[int, dict, bytes]:
    """GET url. Returns (status_code, headers_dict, body_bytes)."""
    req = urllib.request.Request(url, method="GET")
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return resp.status, dict(resp.headers), resp.read()
    except urllib.error.HTTPError as e:
        return e.code, {}, b""
    except Exception as e:
        log.debug(f"GET {url} failed: {e}")
        return 0, {}, b""


def _post(url: str, data: bytes, headers: dict, timeout: int = 15) -> tuple[int, dict, bytes]:
    """POST url with raw bytes body. Returns (status_code, headers_dict, body_bytes)."""
    req = urllib.request.Request(url, data=data, method="POST")
    for k, v in headers.items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return resp.status, dict(resp.headers), resp.read()
    except urllib.error.HTTPError as e:
        return e.code, {}, b""
    except Exception as e:
        log.debug(f"POST {url} failed: {e}")
        return 0, {}, b""


# ── Main class ─────────────────────────────────────────────────────────────


class ResearchNetwork:
    """
    P2P findings exchange for autoresearch agents.

    Wraps the Yggdrasil node HTTP API (default: http://127.0.0.1:9002) to
    broadcast experiment results and receive those of peers.  Maintains an
    in-memory registry of the best result seen from each peer.
    """

    def __init__(self, api_url: str = "http://127.0.0.1:9002"):
        self.api_url = api_url.rstrip("/")
        # peer_id -> best Finding (lowest val_bpb with status "keep") from that peer
        self._peer_best: dict[str, Finding] = {}
        # (sender_id, round_num) already processed — prevents double-counting
        self._seen: set[tuple[str, int]] = set()
        self._our_id: Optional[str] = None

    # ── Identity & discovery ───────────────────────────────────────────────

    def get_topology(self) -> Optional[dict]:
        """Fetch /topology. Returns the raw dict or None on failure."""
        status, _, body = _get(f"{self.api_url}/topology")
        if status == 200:
            try:
                return json.loads(body)
            except json.JSONDecodeError as e:
                log.warning(f"Topology JSON parse error: {e}")
        return None

    def our_id(self) -> Optional[str]:
        """Our Yggdrasil public key, cached after first fetch."""
        if self._our_id is None:
            topo = self.get_topology()
            if topo:
                self._our_id = topo.get("our_public_key")
        return self._our_id

    def all_peer_ids(self) -> list[str]:
        """
        Return all reachable peer IDs in the network.

        Combines direct peers (peers[]) with every node in the spanning tree
        (tree[]) so we reach agents that are not our immediate neighbours.
        Yggdrasil routes the packets regardless of hop distance.
        """
        topo = self.get_topology()
        if not topo:
            return []
        our = topo.get("our_public_key", "")
        ids: set[str] = set()
        for p in topo.get("peers", []):
            if p.get("up") and p.get("public_key"):
                ids.add(p["public_key"])
        for t in topo.get("tree", []):
            if t.get("public_key"):
                ids.add(t["public_key"])
        ids.discard(our)
        return list(ids)

    # ── Sending ────────────────────────────────────────────────────────────

    def broadcast_finding(
        self,
        round_num: int,
        val_bpb: float,
        memory_gb: float,
        status: str,
        description: str,
        commit: str,
        train_py_path: Optional[str] = None,
    ) -> int:
        """
        Broadcast an experiment result to every reachable peer.

        For "keep" results, pass train_py_path so peers can adopt your best
        train.py as their next baseline.  For "discard"/"crash", omit it to
        save bandwidth — the metric alone is still useful signal.

        Returns the number of peers successfully reached.
        """
        train_py: Optional[str] = None
        if train_py_path and status == "keep":
            try:
                with open(train_py_path) as f:
                    train_py = f.read()
            except OSError as e:
                log.warning(f"Could not read {train_py_path}: {e}")

        finding = Finding(
            proto=PROTO_VERSION,
            round_num=round_num,
            val_bpb=val_bpb,
            memory_gb=memory_gb,
            status=status,
            description=description,
            commit=commit,
            timestamp=time.time(),
            sender_id=self.our_id() or "",
            train_py=train_py,
        )

        payload = json.dumps(finding.to_dict()).encode("utf-8")
        peers = self.all_peer_ids()

        if not peers:
            log.warning("No peers reachable — finding not broadcast")
            return 0

        sent = 0
        for peer_id in peers:
            code, _, _ = _post(
                f"{self.api_url}/send",
                data=payload,
                headers={
                    "X-Destination-Peer-Id": peer_id,
                    "Content-Type": "application/octet-stream",
                },
            )
            if code == 200:
                sent += 1
            else:
                log.debug(f"Send to {peer_id[:12]}... returned {code}")

        size_kb = len(payload) / 1024
        log.info(
            f"Round {round_num}: broadcast to {sent}/{len(peers)} peers  "
            f"val_bpb={val_bpb:.6f}  status={status}  "
            f"payload={size_kb:.1f} KB"
            + ("  [+train.py]" if train_py else "")
        )
        return sent

    # ── Receiving ──────────────────────────────────────────────────────────

    def drain_recv_queue(self) -> list[Finding]:
        """
        Pop all pending messages from /recv and return new findings.

        Updates the internal best-per-peer registry.  Silently ignores
        non-finding messages (other protocol types can coexist on the node).
        """
        new_findings: list[Finding] = []

        while True:
            code, headers, body = _get(f"{self.api_url}/recv")

            if code == 204:
                break  # queue empty
            if code != 200:
                if code != 0:
                    log.warning(f"Recv returned {code}")
                break

            sender_id = headers.get("X-From-Peer-Id", headers.get("x-from-peer-id", ""))

            try:
                d = json.loads(body)
            except json.JSONDecodeError:
                log.debug(f"Non-JSON message from {sender_id[:12]}..., skipping")
                continue

            if d.get("type") != MSG_TYPE_FINDING:
                log.debug(f"Ignoring message type '{d.get('type')}' from {sender_id[:12]}...")
                continue

            try:
                finding = Finding.from_dict(d)
            except (KeyError, TypeError, ValueError) as e:
                log.warning(f"Malformed finding from {sender_id[:12]}...: {e}")
                continue

            # Trust the transport-layer sender_id over the payload field
            if sender_id:
                finding.sender_id = sender_id

            key = (finding.sender_id, finding.round_num)
            if key in self._seen:
                continue
            self._seen.add(key)

            # Update best-per-peer registry: only track "keep" with valid bpb
            if finding.status == "keep" and finding.val_bpb > 0:
                existing = self._peer_best.get(finding.sender_id)
                if existing is None or finding.val_bpb < existing.val_bpb:
                    self._peer_best[finding.sender_id] = finding

            new_findings.append(finding)
            log.info(
                f"  peer {finding.sender_id[:16]}...  "
                f"round={finding.round_num}  val_bpb={finding.val_bpb:.6f}  "
                f"{finding.status}  — {finding.description}"
                + ("  [+code]" if finding.train_py else "")
            )

        if new_findings:
            log.info(f"Drained {len(new_findings)} new finding(s) from queue")

        return new_findings

    # ── Decision helpers ───────────────────────────────────────────────────

    def best_peer_finding(self) -> Optional[Finding]:
        """
        Return the best "keep" finding seen from any peer (lowest val_bpb).
        Returns None if no peer findings have been received yet.
        """
        candidates = [
            f for f in self._peer_best.values()
            if f.status == "keep" and f.val_bpb > 0
        ]
        if not candidates:
            return None
        return min(candidates, key=lambda f: f.val_bpb)

    def should_adopt(
        self,
        peer_finding: Finding,
        our_best_bpb: float,
        min_improvement: float = 0.002,
    ) -> bool:
        """
        Return True if we should adopt the peer's train.py as our new baseline.

        Criteria:
          - Finding must be status "keep" with a non-null train_py
          - Peer's val_bpb must beat ours by at least min_improvement
            (default 0.002 — meaningful enough to justify a context switch)
        """
        return (
            peer_finding.status == "keep"
            and peer_finding.train_py is not None
            and peer_finding.val_bpb > 0
            and (our_best_bpb - peer_finding.val_bpb) >= min_improvement
        )

    def peer_registry_summary(self) -> str:
        """Human-readable table of the best result seen from each peer."""
        if not self._peer_best:
            return "No peer findings received yet."
        lines = ["Peer best results:"]
        for pid, f in sorted(self._peer_best.items(), key=lambda x: x[1].val_bpb):
            code_flag = " [+code]" if f.train_py else ""
            lines.append(
                f"  {pid[:16]}...  "
                f"round={f.round_num:3d}  "
                f"val_bpb={f.val_bpb:.6f}  "
                f"{f.status}{code_flag}  — {f.description}"
            )
        return "\n".join(lines)


# ── CLI ────────────────────────────────────────────────────────────────────


def _cli_status(net: ResearchNetwork) -> None:
    """Print topology and peer count."""
    topo = net.get_topology()
    if not topo:
        print("ERROR: could not reach Yggdrasil node")
        raise SystemExit(1)
    peers = net.all_peer_ids()
    print(f"our_id:  {topo['our_public_key']}")
    print(f"our_ipv6:{topo['our_ipv6']}")
    print(f"peers:   {len(peers)}")
    for p in peers:
        print(f"  {p}")


def _cli_recv(net: ResearchNetwork) -> None:
    """Drain recv queue and print each finding as a JSON line."""
    findings = net.drain_recv_queue()
    if not findings:
        print("(no new findings)")
        return
    for f in findings:
        d = f.to_dict()
        d.pop("train_py", None)   # omit source from stdout (can be huge)
        print(json.dumps(d))


def _cli_broadcast(net: ResearchNetwork, args) -> None:
    """Broadcast a single finding from CLI arguments."""
    sent = net.broadcast_finding(
        round_num=args.round,
        val_bpb=args.val_bpb,
        memory_gb=args.memory,
        status=args.status,
        description=args.description,
        commit=args.commit,
        train_py_path=args.train_py,
    )
    print(f"sent to {sent} peer(s)")


def main() -> None:
    import argparse

    parser = argparse.ArgumentParser(
        prog="research_network",
        description="Yggdrasil P2P findings sharing for autoresearch agents",
    )
    parser.add_argument("--api", default="http://127.0.0.1:9002",
                        help="Node API URL (default: http://127.0.0.1:9002)")
    parser.add_argument("-v", "--verbose", action="store_true")
    sub = parser.add_subparsers(dest="cmd", required=True)

    sub.add_parser("status", help="Show topology and reachable peers")
    sub.add_parser("recv",   help="Drain recv queue, print findings as JSON lines")

    bc = sub.add_parser("broadcast", help="Broadcast a finding to all peers")
    bc.add_argument("--round",       type=int,   required=True)
    bc.add_argument("--val-bpb",     type=float, required=True)
    bc.add_argument("--memory",      type=float, required=True, help="Peak VRAM in GB")
    bc.add_argument("--status",      choices=["keep", "discard", "crash"], required=True)
    bc.add_argument("--commit",      required=True, help="7-char git hash")
    bc.add_argument("--description", required=True)
    bc.add_argument("--train-py",    default=None,
                    help="Path to train.py to include (only used when --status keep)")

    args = parser.parse_args()
    logging.basicConfig(
        level=logging.DEBUG if args.verbose else logging.WARNING,
        format="%(levelname)s %(message)s",
    )

    net = ResearchNetwork(api_url=args.api)

    if args.cmd == "status":
        _cli_status(net)
    elif args.cmd == "recv":
        _cli_recv(net)
    elif args.cmd == "broadcast":
        _cli_broadcast(net, args)


if __name__ == "__main__":
    main()
