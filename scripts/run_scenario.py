#!/usr/bin/env python3
"""Run the controlled-event reliability scenario.

Timeline (deterministic):

* P0 (mod 16, start seq 1) - the headline story:
  arrive 1, 4, 3; only 1 delivers; grant a skip proof bound to epoch 1 and
  seq 2; then 3 and 4 deliver; interleave old-epoch-2, new-epoch-2 and
  duplicate acks; resolve both conflicts explicitly; crash before an ack lands
  on disk, recover, and verify stable redelivery ids.

* P1 (mod 8, start seq 6) - wrap-around, memory overflow to spool, an
  injected spool flush failure with retry, and a hot migration carrying the
  still-open gap, its (revoked) proof and all unacknowledged deliveries to
  another owner.

* P2 (mod 16) - stuck downstream for a long stretch: live slots hit the hard
  cap, memory payloads stay bounded, ingress is back-pressured; meanwhile P0
  and P1 keep making progress; after acks resume, P2 drains.
"""

from __future__ import annotations

import sys
from pathlib import Path as _Path

sys.path.insert(0, str(_Path(__file__).resolve().parent.parent))

import argparse
import shutil
from pathlib import Path

from rdl.harness import ScenarioHarness, Trajectory
from rdl.model import PartConfig
from rdl.storage import CommitCrash


def build(root: Path, trajectory_path: Path) -> None:
    if root.exists():
        shutil.rmtree(root)
    root.mkdir(parents=True, exist_ok=True)

    with Trajectory(trajectory_path) as tr:
        h = ScenarioHarness(root, tr, owner="worker-1")

        # ---- configuration ---------------------------------------------
        p0 = PartConfig(
            partition="P0", modulus=16, initial_seq=1, initial_abs=0,
            capacity_live=64, memory_slots=4, inflight_max=8,
        )
        p1 = PartConfig(
            partition="P1", modulus=8, initial_seq=6, initial_abs=5,
            capacity_live=8, memory_slots=2, inflight_max=8,
        )
        # modulus larger than twice the live cap: the hard cap itself is
        # what keeps the live span unambiguous (see docs/design.md).
        p2 = PartConfig(
            partition="P2", modulus=32, initial_seq=1, initial_abs=0,
            capacity_live=9, memory_slots=2, inflight_max=3,
        )
        h.create_partition(p0)
        h.create_partition(p1)
        h.create_partition(p2)

        # =================================================================
        # P0: 1, 4, 3 arrive -> only 1 delivered; P2 marked stuck early so
        # progress on P0/P1 during its stuck window is directly observable.
        # =================================================================
        h.stuck_begin("P2", note="downstream consumer of P2 goes offline")
        for seq in (1, 4, 3):
            h.ingest("P0", 1, seq, payload={"v": f"e{seq}"})
        h.pump("P0", note="gap at seq 2 blocks 3 and 4")
        h.sample("P0")

        # P2 accepts events up to its hard cap; nothing is delivered.
        for seq in range(1, 10):
            h.ingest("P2", 1, seq, payload={"v": seq})
        h.pump("P2", max_deliveries=3, note="first three delivered, then stuck")
        for seq in range(10, 13):
            h.ingest("P2", 1, seq, payload={"v": seq})
        h.sample("P2", note="live at hard cap, memory bounded, disk overflow")

        # P0 progress despite P2 being stuck: ack only 1.
        h.ack("dlv:P0:0", note="ack only event 1")
        h.ack("dlv:P0:0", note="duplicate ack is idempotent")
        h.sample("P0")

        # A proof bound to the *wrong* epoch must be rejected.
        h.note("attempting skip proof with epoch mismatch")
        h.grant_skip("P0", epoch=2, start_seq=2, end_seq=2,
                     note="must be rejected: binds the wrong epoch")

        # The correct proof: epoch 1, exactly the missing range.
        h.grant_skip("P0", 1, 2, 2, note="authorize skipping seq 2")
        h.pump("P0", note="3 and 4 now deliver in order across the proof")
        h.sample("P0")

        # ---- interleaved arrivals after crossing -----------------------
        h.ingest("P0", 1, 2, payload={"v": "old-2-same-epoch"},
                 note="original event arrives after the gap was crossed")
        h.ingest("P0", 0, 2, payload={"v": "older-epoch-2"},
                 note="event from an older epoch arrives late")
        h.ingest("P2", 1, 13, note="back-pressured: past P2 hard cap")
        h.sample("P2")

        # P1 keeps making progress while P2 is stuck and P0 has conflicts.
        for seq in (6, 7, 0):
            h.ingest("P1", 1, seq, payload={"w": seq})
        h.pump("P1", note="P1 wraps 6,7,0,1... gap at seq 1")
        for seq in (1,):
            h.ingest("P1", 1, seq, payload={"w": seq})
        h.pump("P1")
        h.ack("dlv:P1:5")
        h.ack("dlv:P1:6")
        h.sample("P1", note="P1 advanced independently of stuck P2")

        # ---- resolve P0 conflicts explicitly ----------------------------
        conflicts = {c.seq: c for c in h.open_conflicts("P0")}
        same_epoch = next(
            c for c in h.open_conflicts("P0") if c.epoch == 1
        )
        old_epoch = next(
            c for c in h.open_conflicts("P0") if c.epoch == 0
        )
        h.resolve_conflict("P0", same_epoch.conflict_id, "confirm_skip",
                           note="original 2 arrives after cross: keep the skip")
        h.resolve_conflict("P0", old_epoch.conflict_id, "confirm_skip",
                           note="stale epoch event: keep the skip")
        h.pump("P0", note="skip-confirmed corrections emitted")
        h.ack(f"corr:P0:{same_epoch.ordinal}")
        h.ack(f"corr:P0:{old_epoch.ordinal}")

        # A new epoch claims the skipped position: real divergence, and it
        # gets explicitly reinstated as an out-of-band correction.
        h.ingest("P0", 2, 2, payload={"v": "new-epoch-2"},
                 note="new epoch produces the skipped seq 2")
        new_epoch = next(
            c for c in h.open_conflicts("P0") if c.epoch == 2
        )
        h.resolve_conflict("P0", new_epoch.conflict_id, "reinstate",
                           note="deliver the new-epoch event as a correction")
        h.pump("P0", note="reinstate correction delivered")
        h.ack(f"corr:P0:{new_epoch.ordinal}")
        h.sample("P0")

        # ---- crash before ack lands ------------------------------------
        h.arm_commit_crash(1, note="next ack commit crashes before durability")
        crashed_id = "dlv:P0:2"
        try:
            h.ack(crashed_id, note="ack for event 3 lost in the crash")
        except CommitCrash:
            pass
        h.crash_and_recover(note="process restart from spool + wal")
        h.pump("P0", note="1 must not redeliver; 3 and 4 redeliver with same ids")
        h.ack("dlv:P0:2")
        h.ack("dlv:P0:3")
        h.sample("P0", note="P0 fully confirmed through seq 4")

        # =================================================================
        # P1: overflow to spool, injected spool failure, wrap gap, migration
        # carrying a revoked proof.
        # =================================================================
        # force memory overflow and one failed flush
        h.arm_spill_failure("P1", 1, note="next overflow flush fails")
        rec = h.ingest("P1", 1, 2, payload={"w": 2},
                       note="overflow: flush fails once, ingress rejected")
        # retry after the failure is disarmed - same event is accepted
        h.ingest("P1", 1, 2, payload={"w": 2}, note="retry after spill failure")
        h.ingest("P1", 1, 3, payload={"w": 3})
        h.pump("P1")
        for did in ("dlv:P1:7", "dlv:P1:8", "dlv:P1:9", "dlv:P1:10"):
            h.ack(did)
        h.sample("P1", note="acked payloads compacted, spool bounded")

        # gap at seq 4 (mod 8): prove it, then revoke *before* crossing
        h.grant_skip("P1", 1, 4, 4, proof_id="proof:P1:gap4",
                     note="pre-cross proof, will be revoked on the new owner")
        h.sample("P1")

        # controlled hot migration while gap/proof/unacked state exists
        bundle = root.parent / "bundles" / "P1"
        h.migrate("P1", bundle, "worker-2",
                  note="migrate gaps, proofs and ack state to worker-2")
        h.pump("P1", note="frozen then resumed: redeliveries after migration")
        h.sample("P1")

        # revoke on the new owner before the gap is crossed
        h.revoke_skip("P1", "proof:P1:gap4",
                      note="revocation after migration but before crossing")
        h.ingest("P1", 1, 4, payload={"w": 4}, note="real event fills the gap")
        h.pump("P1", note="P1 delivers 4, then continues to wrap onward")
        h.ack("dlv:P1:11", note="ack the unacked 4 after migration")
        h.ingest("P1", 1, 5, payload={"w": 5})
        h.pump("P1")
        h.ack("dlv:P1:12")
        h.sample("P1", note="P1 drained after migration")

        # =================================================================
        # P2: downstream comes back; the stuck partition drains, and the
        # back-pressured ingress is now accepted.
        # =================================================================
        h.stuck_end("P2", note="downstream recovers")

        def drain(batch: tuple[str, str, str]) -> None:
            for did in batch:
                h.ack(did)
            h.pump("P2", max_deliveries=3)

        # free the three in-flight slots; buffered 4..9 shift into delivery
        drain(("dlv:P2:0", "dlv:P2:1", "dlv:P2:2"))
        # live count has dropped to 6 (4..9): the source retries the
        # back-pressured 10..13; the first three fit, 13 waits one more ack.
        for seq in (10, 11, 12):
            h.ingest("P2", 1, seq, payload={"v": seq},
                     note=f"retry of back-pressured seq {seq}")
        h.ingest("P2", 1, 13, payload={"v": 13},
                 note="still back-pressured until one more slot frees")
        drain(("dlv:P2:3", "dlv:P2:4", "dlv:P2:5"))
        h.ingest("P2", 1, 13, payload={"v": 13},
                 note="retry of seq 13 now admitted")
        drain(("dlv:P2:6", "dlv:P2:7", "dlv:P2:8"))
        drain(("dlv:P2:9", "dlv:P2:10", "dlv:P2:11"))
        # inflight cap freed the last slot: pump offers retried seq 13
        h.pump("P2", max_deliveries=3, note="the final buffered event delivers")
        h.ack("dlv:P2:12")
        h.pump("P2", max_deliveries=3, note="no remaining gap: pump is idle")
        h.sample("P2", note="P2 drained; live and spool counts reclaimed")

        h.note("scenario complete")


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--root", default="runtime/scenario")
    ap.add_argument("--trajectory", default="runtime/trajectory.jsonl")
    args = ap.parse_args()
    build(Path(args.root), Path(args.trajectory))
    print(f"state:   {args.root}")
    print(f"trace:   {args.trajectory}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
