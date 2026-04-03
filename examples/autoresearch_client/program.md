# autoresearch

This is a collaborative experiment: multiple agents, each on their own GPU, run the research
loop simultaneously and share findings over a P2P network.  When any agent finds a better
`train.py`, all peers can adopt it as their new baseline and keep improving from there.

## Setup

To set up a new experiment, work with the user to:

1. **Agree on a run tag**: propose a tag based on today's date and a short identifier
   (e.g. `apr3-alice`).  The branch `autoresearch/<tag>` must not already exist.
2. **Create the branch**: `git checkout -b autoresearch/<tag>` from current master.
3. **Read the in-scope files**: The repo is small.  Read these files for full context:
   - `README.md` — repository context.
   - `prepare.py` — fixed constants, data prep, tokenizer, dataloader, evaluation.  Do not modify.
   - `train.py` — the file you modify.  Model architecture, optimizer, training loop.
4. **Verify data exists**: Check that `~/.cache/autoresearch/` contains data shards and a
   tokenizer.  If not, tell the human to run `uv run prepare.py`.
5. **Initialize results.tsv**: Create `results.tsv` with just the header row.  The baseline
   will be recorded after the first run.
6. **Check network connectivity**: Run
   ```
   python ../yggdrasil-node/examples/autoresearch_client/research_network.py status
   ```
   If the node is reachable it will print your peer ID and a list of connected peers.
   If it fails (node not running), continue anyway — the network calls are non-fatal and
   you will just operate as a solo agent until the node comes up.
7. **Confirm and go**: Confirm setup looks good.

Once you get confirmation, kick off the experimentation.

## Experimentation

Each experiment runs on a single GPU.  The training script runs for a **fixed time budget of
5 minutes** (wall clock training time, excluding startup/compilation).  You launch it simply as:
`uv run train.py`.

**What you CAN do:**
- Modify `train.py` — this is the only file you edit.  Everything is fair game: model
  architecture, optimizer, hyperparameters, training loop, batch size, model size, etc.

**What you CANNOT do:**
- Modify `prepare.py`.  It is read-only.  It contains the fixed evaluation, data loading,
  tokenizer, and training constants (time budget, sequence length, etc).
- Install new packages or add dependencies.  You can only use what's already in `pyproject.toml`.
- Modify the evaluation harness.  The `evaluate_bpb` function in `prepare.py` is the ground
  truth metric.

**The goal is simple: get the lowest val_bpb.**  Since the time budget is fixed, you don't need
to worry about training time — it's always 5 minutes.  Everything is fair game: change the
architecture, the optimizer, the hyperparameters, the batch size, the model size.  The only
constraint is that the code runs without crashing and finishes within the time budget.

**VRAM** is a soft constraint.  Some increase is acceptable for meaningful val_bpb gains, but
it should not blow up dramatically.

**Simplicity criterion**: All else being equal, simpler is better.  A small improvement that adds
ugly complexity is not worth it.  Conversely, removing something and getting equal or better
results is a great outcome — that's a simplification win.  When evaluating whether to keep a
change, weigh the complexity cost against the improvement magnitude.  A 0.001 val_bpb improvement
that adds 20 lines of hacky code?  Probably not worth it.  A 0.001 val_bpb improvement from
deleting code?  Definitely keep.  An improvement of ~0 but much simpler code?  Keep.

**The first run**: Your very first run should always be to establish the baseline, so you will
run the training script as is.

## Output format

Once the script finishes it prints a summary like this:

```
---
val_bpb:          0.997900
training_seconds: 300.1
total_seconds:    325.9
peak_vram_mb:     45060.2
mfu_percent:      39.80
total_tokens_M:   499.6
num_steps:        953
num_params_M:     50.3
depth:            8
```

Extract the key metrics from the log file:

```
grep "^val_bpb:\|^peak_vram_mb:" run.log
```

## Logging results

When an experiment is done, log it to `results.tsv` (tab-separated, NOT comma-separated —
commas break in descriptions).

The TSV has a header row and 5 columns:

```
commit	val_bpb	memory_gb	status	description
```

1. git commit hash (short, 7 chars)
2. val_bpb achieved (e.g. 1.234567) — use 0.000000 for crashes
3. peak memory in GB, round to .1f (e.g. 12.3 — divide peak_vram_mb by 1024) — use 0.0 for crashes
4. status: `keep`, `discard`, or `crash`
5. short text description of what this experiment tried

Example:

```
commit	val_bpb	memory_gb	status	description
a1b2c3d	0.997900	44.0	keep	baseline
b2c3d4e	0.993200	44.2	keep	increase LR to 0.04
c3d4e5f	1.005000	44.0	discard	switch to GeLU activation
d4e5f6g	0.000000	0.0	crash	double model width (OOM)
e5f6g7h	0.989100	44.3	keep	adopt: warmup 100 steps [peer 3f8a2c1d]
```

Adopted rounds are recorded normally — `adopt:` prefix in the description is just for
your own bookkeeping.

## The experiment loop

The experiment runs on a dedicated branch (e.g. `autoresearch/apr3-alice`).

LOOP FOREVER:

1. Look at the git state: the current branch/commit we're on.

2. **Check the network for peer findings** (non-fatal if node is unreachable):
   ```
   python ../yggdrasil-node/examples/autoresearch_client/research_network.py recv
   ```
   This prints any new findings from peers as JSON lines.  Each line includes `val_bpb`,
   `status`, `description`, `sender_id`, and whether `train_py` is attached.

3. **Decide what to run this round.**  Two cases:

   **A) Adopt a peer baseline** — if a peer's finding has `status: keep` and their `val_bpb`
   beats your current best by **≥ 0.002**, adopt their `train.py` using the library:
   ```python
   import sys; sys.path.insert(0, '../yggdrasil-node/examples/autoresearch_client')
   from research_network import ResearchNetwork
   net = ResearchNetwork()
   net.drain_recv_queue()
   best = net.best_peer_finding()
   if best and net.should_adopt(best, YOUR_BEST_BPB):
       best.write_train_py('train.py')
   ```
   Treat this as a normal experiment round: run it, record it, keep or revert as usual.
   If the adopted code crashes or underperforms, revert and continue your own line — never
   trust a peer result blindly.

   **B) Your own experiment** — tune `train.py` with a new idea by directly hacking the code.

4. `git commit` the modified `train.py`.

5. Run the experiment: `uv run train.py > run.log 2>&1`
   (redirect everything — do NOT use tee or let output flood your context)

6. Read out the results: `grep "^val_bpb:\|^peak_vram_mb:" run.log`

7. If the grep output is empty, the run crashed.  Run `tail -n 50 run.log` to read the
   Python stack trace and attempt a fix.  If you can't get things to work after more than
   a few attempts, give up.

8. Record the results in the tsv (NOTE: do not commit the results.tsv file, leave it untracked).

9. If val_bpb improved (lower), you "advance" the branch, keeping the git commit.
   If val_bpb is equal or worse, `git reset --hard HEAD~1` back to where you started.

10. **Broadcast your result to the network** (non-fatal if node is unreachable):
    ```
    python ../yggdrasil-node/examples/autoresearch_client/research_network.py broadcast \
        --round ROUND_NUM \
        --val-bpb VAL_BPB \
        --memory MEMORY_GB \
        --status STATUS \
        --commit COMMIT_HASH \
        --description "DESCRIPTION" \
        --train-py train.py
    ```
    Pass `--train-py train.py` only when `--status keep`; omit it for discard/crash to
    save bandwidth.  Peers will only be able to adopt your code if you include it.

11. Repeat from step 1.

---

The idea is that you are a completely autonomous researcher trying things out.  If they work,
keep.  If they don't, discard.  You advance the branch on improvements and share every result
— good or bad — so peers have an accurate picture of the search space.  If you feel like you're
getting stuck, you can rewind but do this very sparingly (if ever).

**Timeout**: Each experiment should take ~5 minutes total (+ a few seconds for startup and eval
overhead).  If a run exceeds 10 minutes, kill it and treat it as a failure.

**Crashes**: If a run crashes (OOM, or a bug, or etc.), use your judgment: if it's something
dumb and easy to fix (e.g. a typo, a missing import), fix it and re-run.  If the idea itself
is fundamentally broken, log `crash` and move on.

**NEVER STOP**: Once the experiment loop has begun (after the initial setup), do NOT pause to
ask the human if you should continue.  Do NOT ask "should I keep going?" or "is this a good
stopping point?".  The human might be asleep, or gone from a computer and expects you to
continue working *indefinitely* until you are manually stopped.  You are autonomous.  If you
run out of ideas, think harder — read papers referenced in the code, re-read the in-scope files
for new angles, try combining previous near-misses, try more radical architectural changes.
The loop runs until the human interrupts you, period.

The network calls (steps 2 and 10) take under a second each.  They do not meaningfully slow
down the loop.  A crashed or unreachable node must never cause the loop to stall — if a network
call fails, log a warning and continue.

## Network reference

The `research_network.py` script lives in `../yggdrasil-node/examples/autoresearch_client/`
relative to the autoresearch repo.  It requires no external dependencies.

Key CLI commands:

| Command | What it does |
|---------|-------------|
| `... status` | Print our peer ID, IPv6, and all reachable peers |
| `... recv` | Drain the recv queue; print new findings as JSON lines |
| `... broadcast --round N --val-bpb X ...` | Send this round's result to all peers |

Pass `--api http://127.0.0.1:9002` if the node runs on a non-default port.

Peers are identified by their 64-char hex Yggdrasil public key.  Two agents are on the same
network if they connect to any common bootstrap peer — no manual exchange of addresses needed.
The included `node-config.json` uses public bootstrap servers, so anyone who runs the node
joins the same overlay automatically.
