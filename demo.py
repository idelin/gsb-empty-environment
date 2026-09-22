"""End-to-end scenario for the windowed result merger.

Scenario (per requirements):
  * A v2=5 with stale A v1=9 and compatible B v1=3  -> window w1 = 8
  * B on another rule version                        -> window w2 stays pending
  * A splits into A1/A2 (handoff manifest); old-A and new-sub-shard
    arrivals interleave; no double counting           -> window w3
  * process killed between publish-record write and downstream ack
    -> recovery re-delivers the SAME result id, downstream delta stays
       idempotent
  * post-publish correction -> compensation event, history untouched
  * revocation and timeout handled as their own outcomes
  * independent validator audits the state dir afterwards

Run:  python3 demo.py [workdir]
"""
from __future__ import annotations

import multiprocessing as mp
import os
import shutil
import sys

from winmerge.downstream import FileDownstream
from winmerge.merger import WindowMerger, WindowSpec
from winmerge import validator

WINDOWS = [
    WindowSpec("w1", 0, 100, deadline=1000),
    WindowSpec("w2", 100, 200, deadline=1000),
    WindowSpec("w3", 200, 300, deadline=1000),
    WindowSpec("w4", 300, 400, deadline=500),
]
SHARDS = ["A", "B"]


def show(actions):
    for a in actions:
        print("   ", a)


def child_phase(state_dir: str, downstream_path: str) -> None:
    """Runs the merger until w3 publishes, then dies mid-ack."""
    downstream = FileDownstream(downstream_path)

    def crash_hook(stage, payload):
        # die after the w3 publish record is fsynced and delivered, but
        # before the merger records the downstream ack
        if stage == "after_delivery" and payload["window"] == "w3":
            print("    *** CRASH: process killed between publish record "
                  "and downstream ack ***", flush=True)
            os._exit(1)

    m = WindowMerger(state_dir, WINDOWS, SHARDS, downstream, crash_hook)

    print("[child] w1: A v2=5, stale A v1=9, B v1=3 (rules r1)")
    show(m.handle({"type": "partial", "window": "w1", "shard": "A", "epoch": 1,
                   "local_version": 2, "watermark": 10, "rule_version": "r1",
                   "value": 5}))
    show(m.handle({"type": "partial", "window": "w1", "shard": "A", "epoch": 1,
                   "local_version": 1, "watermark": 10, "rule_version": "r1",
                   "value": 9}))
    show(m.handle({"type": "partial", "window": "w1", "shard": "B", "epoch": 1,
                   "local_version": 1, "watermark": 20, "rule_version": "r1",
                   "value": 3}))

    print("[child] w2: B arrives on a DIFFERENT rule version -> pending")
    show(m.handle({"type": "partial", "window": "w2", "shard": "A", "epoch": 1,
                   "local_version": 1, "watermark": 110, "rule_version": "r1",
                   "value": 1}))
    show(m.handle({"type": "partial", "window": "w2", "shard": "B", "epoch": 1,
                   "local_version": 1, "watermark": 120, "rule_version": "r2",
                   "value": 7}))

    print("[child] scale-out: handoff manifest m2 splits A -> A1, A2 @epoch2")
    show(m.handle({"type": "handoff", "manifest_id": "m2", "epoch": 2,
                   "splits": {"A": ["A1", "A2"]}}))

    print("[child] w3: old-A and new sub-shard arrivals interleaved")
    show(m.handle({"type": "partial", "window": "w3", "shard": "A", "epoch": 1,
                   "local_version": 3, "watermark": 210, "rule_version": "r1",
                   "value": 100}))
    show(m.handle({"type": "partial", "window": "w3", "shard": "A1", "epoch": 2,
                   "local_version": 1, "watermark": 210, "rule_version": "r1",
                   "value": 1}))
    show(m.handle({"type": "partial", "window": "w3", "shard": "A", "epoch": 1,
                   "local_version": 4, "watermark": 210, "rule_version": "r1",
                   "value": 100}))
    show(m.handle({"type": "partial", "window": "w3", "shard": "A2", "epoch": 2,
                   "local_version": 1, "watermark": 220, "rule_version": "r1",
                   "value": 2}))
    show(m.handle({"type": "partial", "window": "w3", "shard": "B", "epoch": 2,
                   "local_version": 1, "watermark": 230, "rule_version": "r1",
                   "value": 4}))
    print("[child] unexpectedly still alive!", flush=True)


def main() -> int:
    workdir = sys.argv[1] if len(sys.argv) > 1 else "demo_state"
    state_dir = os.path.join(workdir, "merger")
    downstream_path = os.path.join(workdir, "downstream.json")
    shutil.rmtree(workdir, ignore_errors=True)
    os.makedirs(state_dir)

    print("=" * 72)
    print("PHASE 1: child process ingests slices, crashes mid-publish of w3")
    print("=" * 72)
    ctx = mp.get_context("spawn")
    proc = ctx.Process(target=child_phase, args=(state_dir, downstream_path))
    proc.start()
    proc.join()
    assert proc.exitcode == 1, f"expected crash, got exit {proc.exitcode}"

    print()
    print("=" * 72)
    print("PHASE 2: recovery in a fresh process (state replayed from log)")
    print("=" * 72)
    downstream = FileDownstream(downstream_path)
    m = WindowMerger(state_dir, WINDOWS, SHARDS, downstream)
    crashed_ids = [
        p["result_id"] for p in m.windows["w3"].publishes
    ]
    print(f"    publish record found in log, unacked: {crashed_ids}")
    print(f"    downstream already applied: {downstream.applied_ids()}")
    redelivered = m.recover()
    print(f"    recovery re-delivered: {redelivered}")
    assert redelivered == crashed_ids, "result id changed across recovery!"
    assert downstream.applied_ids().count(crashed_ids[0]) == 1
    print(f"    downstream w3 total (idempotent, applied once): "
          f"{downstream.window_total('w3')}")
    assert downstream.window_total("w3") == 7  # A1=1 + A2=2 + B=4, old A ignored

    print()
    print("=" * 72)
    print("PHASE 3: post-publish correction -> compensation (no history rewrite)")
    print("=" * 72)
    snap_path = os.path.join(workdir, "snap.json")
    validator.snapshot(state_dir, snap_path)
    show(m.handle({"type": "partial", "window": "w3", "shard": "A2", "epoch": 2,
                   "local_version": 2, "watermark": 220, "rule_version": "r1",
                   "value": 5}))
    print(f"    downstream w3 total after compensation: "
          f"{downstream.window_total('w3')}")
    ok = validator.verify_snapshot(state_dir, snap_path)
    print(f"    historical snapshot still valid after correction: {ok}")
    assert ok

    print()
    print("=" * 72)
    print("PHASE 4: revocation, duplicate/unknown acks, timeout")
    print("=" * 72)
    print("[revoke] B v1 of published w1 is revoked")
    show(m.handle({"type": "revoke", "window": "w1", "shard": "B", "epoch": 1,
                   "local_version": 1, "reason": "upstream retracted"}))
    print(f"    downstream w1 total after retraction: "
          f"{downstream.window_total('w1')}")
    print("[ack] duplicate + unknown acks")
    w1_result = m.windows["w1"].publishes[0]["result_id"]
    show(m.handle({"type": "ack", "result_id": w1_result}))
    show(m.handle({"type": "ack", "result_id": "res-does-not-exist"}))
    print("[timeout] w4 gets only A1, then the deadline passes")
    show(m.handle({"type": "partial", "window": "w4", "shard": "A1", "epoch": 2,
                   "local_version": 1, "watermark": 310, "rule_version": "r1",
                   "value": 1}))
    show(m.handle({"type": "tick", "now": 600}))
    show(m.handle({"type": "partial", "window": "w4", "shard": "B", "epoch": 2,
                   "local_version": 1, "watermark": 320, "rule_version": "r1",
                   "value": 2}))

    print()
    print("=" * 72)
    print("PHASE 5: independent validator audit")
    print("=" * 72)
    rc = validator.main(["check", state_dir, "--initial-shards", "A,B"])
    print()
    print("downstream totals:",
          {w: downstream.window_total(w) for w in ("w1", "w2", "w3", "w4")})
    return rc


if __name__ == "__main__":
    sys.exit(main())
