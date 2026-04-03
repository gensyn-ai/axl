"""
agent_loop.py — Reference integration of ResearchNetwork with the autoresearch loop.

This is a complete, runnable example showing how to weave peer sharing into
the experiment loop from program.md.  It mirrors that spec exactly, adding
network calls at two points per round:

  START of each round:
    - drain_recv_queue() to collect peer findings
    - optionally adopt a peer's train.py if it's significantly better than ours

  END of each round:
    - broadcast_finding() to share our result with all peers

Adoption rationale
------------------
When a peer's val_bpb beats ours by >= ADOPT_THRESHOLD (default 0.002), we
replace train.py with their source and treat it as a normal experiment round:
run it, record it, keep or revert as usual.  This means:

  - We never blindly trust a peer — every adoption is validated by actually running
  - If the adopted code crashes or underperforms, we revert and continue our own line
  - A successful adoption advances our branch and we keep improving from that baseline
  - Adopted rounds are marked in results.tsv with "adopt: ..." in the description

Run from the autoresearch directory:

    cd /path/to/autoresearch
    python /path/to/examples/autoresearch_client/agent_loop.py

Or pass the autoresearch directory as an argument:

    python agent_loop.py --dir /path/to/autoresearch
"""

import argparse
import logging
import subprocess
import sys
import time
from pathlib import Path

# Allow running from any directory
sys.path.insert(0, str(Path(__file__).parent))
from research_network import ResearchNetwork

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s  %(levelname)-7s  %(message)s",
    datefmt="%H:%M:%S",
)
log = logging.getLogger(__name__)

# ── Defaults (override with CLI args or env vars) ──────────────────────────
DEFAULT_AUTORESEARCH_DIR = Path(__file__).parent.parent.parent / "autoresearch"
DEFAULT_API_URL = "http://127.0.0.1:9002"
ADOPT_THRESHOLD = 0.002   # min val_bpb gap to adopt a peer's train.py
RUN_TIMEOUT_SECS = 700    # 10-min hard kill — matches program.md spec


# ── Helpers ────────────────────────────────────────────────────────────────

def run_experiment(work_dir: Path) -> tuple[float, float]:
    """
    Execute train.py and return (val_bpb, memory_gb).
    Returns (0.0, 0.0) on crash or timeout.
    """
    log.info("Starting training run (~5 min budget)...")
    run_log = work_dir / "run.log"
    try:
        proc = subprocess.run(
            ["uv", "run", "train.py"],
            cwd=work_dir,
            capture_output=True,
            text=True,
            timeout=RUN_TIMEOUT_SECS,
        )
        run_log.write_text(proc.stdout + proc.stderr)
        output = proc.stdout
    except subprocess.TimeoutExpired:
        log.warning(f"Run exceeded {RUN_TIMEOUT_SECS}s — treating as crash")
        return 0.0, 0.0
    except Exception as e:
        log.error(f"Subprocess failed: {e}")
        return 0.0, 0.0

    val_bpb = 0.0
    memory_gb = 0.0
    for line in output.splitlines():
        if line.startswith("val_bpb:"):
            val_bpb = float(line.split(":", 1)[1].strip())
        elif line.startswith("peak_vram_mb:"):
            memory_gb = round(float(line.split(":", 1)[1].strip()) / 1024, 1)

    if val_bpb == 0.0:
        log.warning("val_bpb not found in output — treating as crash")
        log.warning(f"Last 20 lines of run.log:\n" +
                    "\n".join(output.splitlines()[-20:]))

    return val_bpb, memory_gb


def git_commit(work_dir: Path, message: str) -> str:
    """Stage train.py, commit, and return the short hash."""
    subprocess.run(["git", "add", "train.py"], cwd=work_dir, check=True)
    subprocess.run(["git", "commit", "-m", message], cwd=work_dir, check=True)
    result = subprocess.run(
        ["git", "rev-parse", "--short", "HEAD"],
        cwd=work_dir, capture_output=True, text=True, check=True,
    )
    return result.stdout.strip()


def git_revert(work_dir: Path) -> None:
    """Hard-reset to the previous commit, discarding the current train.py."""
    subprocess.run(["git", "reset", "--hard", "HEAD~1"], cwd=work_dir, check=True)


def log_result(
    results_tsv: Path,
    commit: str,
    val_bpb: float,
    memory_gb: float,
    status: str,
    description: str,
) -> None:
    """Append a row to results.tsv, creating it with a header if missing."""
    if not results_tsv.exists():
        results_tsv.write_text("commit\tval_bpb\tmemory_gb\tstatus\tdescription\n")
    with open(results_tsv, "a") as f:
        f.write(f"{commit}\t{val_bpb:.6f}\t{memory_gb:.1f}\t{status}\t{description}\n")
    log.info(
        f"Recorded: {commit}  val_bpb={val_bpb:.6f}  "
        f"mem={memory_gb:.1f}GB  {status}  — {description}"
    )


# ── Main loop ──────────────────────────────────────────────────────────────

def main(work_dir: Path, api_url: str) -> None:
    results_tsv = work_dir / "results.tsv"
    train_py    = work_dir / "train.py"

    net = ResearchNetwork(api_url=api_url)

    # Print our identity and peer count on startup
    topo = net.get_topology()
    if topo:
        peers = net.all_peer_ids()
        log.info(
            f"Node: {topo['our_public_key'][:24]}...  "
            f"reachable peers: {len(peers)}"
        )
        if peers:
            log.info("  " + "  ".join(p[:16] + "..." for p in peers[:5]))
    else:
        log.warning(
            "Could not reach Yggdrasil node at %s — "
            "running without network sharing", api_url
        )

    if not results_tsv.exists():
        results_tsv.write_text("commit\tval_bpb\tmemory_gb\tstatus\tdescription\n")

    best_bpb  = float("inf")   # our personal best val_bpb
    round_num = 0

    log.info("Experiment loop started. Ctrl+C to stop.\n")

    while True:
        round_num += 1
        sep = "─" * 60
        log.info(f"\n{sep}\nROUND {round_num}  "
                 f"(our best: {'∞' if best_bpb == float('inf') else f'{best_bpb:.6f}'})"
                 f"\n{sep}")

        # ── Step 1: collect peer findings ─────────────────────────────
        new_findings = net.drain_recv_queue()
        if new_findings:
            log.info(net.peer_registry_summary())

        # ── Step 2: decide what to run this round ─────────────────────
        #
        # Two cases:
        #   A) Adopt a peer's train.py (if their best beats ours by >= threshold)
        #   B) Make our own experimental change  ← an LLM fills this in
        #
        # This reference script handles case A automatically.
        # For case B it just runs the current train.py unchanged, because
        # actually generating an experimental edit requires an LLM call that
        # lives outside this file (see program.md for the full agent spec).

        best_peer = net.best_peer_finding()
        description: str

        if best_peer and net.should_adopt(best_peer, best_bpb, ADOPT_THRESHOLD):
            log.info(
                f"Adopting train.py from peer {best_peer.sender_id[:16]}...  "
                f"their val_bpb={best_peer.val_bpb:.6f}  ours={best_bpb:.6f}"
            )
            best_peer.write_train_py(str(train_py))
            description = (
                f"adopt: {best_peer.description} "
                f"[peer {best_peer.sender_id[:8]}]"
            )
        else:
            # ← INSERT LLM-GENERATED EDIT TO train.py HERE ←
            #
            # Example (pseudocode):
            #   idea = llm.generate_experiment_idea(current_train_py, results_tsv)
            #   apply_edit(train_py, idea.diff)
            #   description = idea.description
            #
            # For now we just run the current file so the scaffolding is testable.
            description = "no-op reference run"

        # ── Step 3: commit ────────────────────────────────────────────
        try:
            commit = git_commit(work_dir, description)
        except subprocess.CalledProcessError as e:
            log.error(f"git commit failed: {e}")
            continue

        # ── Step 4: run experiment ────────────────────────────────────
        val_bpb, memory_gb = run_experiment(work_dir)

        # ── Step 5: keep or discard ───────────────────────────────────
        if val_bpb == 0.0:
            status = "crash"
            git_revert(work_dir)
        elif val_bpb < best_bpb:
            status = "keep"
            best_bpb = val_bpb
        else:
            status = "discard"
            git_revert(work_dir)

        # ── Step 6: record in results.tsv ─────────────────────────────
        log_result(results_tsv, commit, val_bpb, memory_gb, status, description)

        # ── Step 7: broadcast to the network ──────────────────────────
        # Include train.py source only for "keep" so peers can adopt it.
        train_py_path = str(train_py) if status == "keep" else None
        sent = net.broadcast_finding(
            round_num=round_num,
            val_bpb=val_bpb,
            memory_gb=memory_gb,
            status=status,
            description=description,
            commit=commit,
            train_py_path=train_py_path,
        )
        if sent:
            log.info(f"Broadcast to {sent} peer(s)")

        time.sleep(0.5)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(
        description="Autoresearch agent loop with P2P findings sharing"
    )
    parser.add_argument(
        "--dir",
        type=Path,
        default=DEFAULT_AUTORESEARCH_DIR,
        help="Path to the autoresearch working directory (default: ../../autoresearch)",
    )
    parser.add_argument(
        "--api",
        default=DEFAULT_API_URL,
        help=f"Yggdrasil node HTTP API URL (default: {DEFAULT_API_URL})",
    )
    args = parser.parse_args()

    if not args.dir.exists():
        print(f"Error: autoresearch directory not found: {args.dir}", file=sys.stderr)
        sys.exit(1)

    main(args.dir, args.api)
