"""Runnable, scripted version of the required scenario.

Produces node1/node2 state and trace.jsonl under ./run, then runs the
delivery-trace verifier. Equivalent to tests/test_scenario.py but with
narrative output for inspection.
"""
from __future__ import annotations

import sys
import shutil
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(ROOT))

from reliable_delivery import (
    CapacityError,
    DeliveryLayer,
    Event,
    PartitionConfig,
    SpillError,
)
from tools.verify_trace import verify_trace

RUN = ROOT / "run"


def ev(partition, epoch, seq):
    return Event(partition, epoch, seq, f"payload-{partition}-{epoch}-{seq}")


def show(tag, delivered):
    ids = ", ".join(e.delivery_id for e in delivered) or "-"
    print(f"  {tag}: {ids}")


def main() -> int:
    shutil.rmtree(RUN, ignore_errors=True)
    config = PartitionConfig(modulus=256, max_buffered=4, max_spilled=4)
    node1 = DeliveryLayer(RUN / "node1", config)
    node2 = DeliveryLayer(RUN / "node2", config)

    print("== 1,4,3 arrive; only 1 acked; skip-proof for 2 releases 3,4")
    show("deliver", node1.ingest(ev("A", 0, 1)))
    node1.ingest(ev("A", 0, 4))
    node1.ingest(ev("A", 0, 3))
    node1.ack("A#0:0:1")
    _, delivered = node1.authorize_skip("A", 0, 2, 2)
    show("deliver", delivered)

    print("== late old-epoch 2 -> conflict, corrected as an explicit delivery")
    node1.ingest(ev("A", 0, 2))
    conflict = node1.conflicts("A")[0]
    print(f"  conflict: {conflict.kind} ({conflict.conflict_id})")
    corrected = node1.resolve_conflict("A", conflict.conflict_id, "deliver")
    show("correction", [corrected])

    print("== new epoch; 0,2,1 interleave")
    node1.advance_epoch("A", 1)
    show("deliver", node1.ingest(ev("A", 1, 0)))
    node1.ingest(ev("A", 1, 2))
    show("deliver", node1.ingest(ev("A", 1, 1)))
    node1.ack("A#0:0:1")  # duplicate ack -> no-op

    print("== spill failure on C, then retry")
    node1.ingest(ev("C", 0, 1))
    for seq in (3, 4, 5, 6):
        node1.ingest(ev("C", 0, seq))
    node1.spill.fail_next_writes = 1
    try:
        node1.ingest(ev("C", 0, 7))
    except SpillError:
        print("  spill failure injected -> backpressure, retrying")
    node1.ingest(ev("C", 0, 7))  # accepted to spill; awaits gap
    print("  retry accepted (held behind gap 2)")
    show("after skip 2", node1.authorize_skip("C", 0, 2, 2)[1])

    print("== crash before acks are persisted, then recover")
    node1.flush()
    node1.ack("A#1:0:0")
    node1.ack("A#1:0:1")
    node1.crash()
    show("redelivered", node1.recover())
    node1.flush()

    print("== partition D stuck on gap 2; A still progresses; capacity bounded")
    show("deliver", node1.ingest(ev("D", 0, 1)))
    node1.ack("D#0:0:1")
    for seq in (3, 4, 5, 6, 10, 11, 12, 13):
        node1.ingest(ev("D", 0, seq))
    try:
        node1.ingest(ev("D", 0, 14))
    except CapacityError:
        print("  D capacity exhausted -> event refused (bounded)")
    show("A progresses", node1.ingest(ev("A", 1, 3)))

    print("== hot migration of D; carried proof for [7,9] resumes delivery")
    node1.authorize_skip("D", 0, 7, 9)
    node2.import_partition(node1.export_partition("D"))
    show("node2 after skip 2", node2.authorize_skip("D", 0, 2, 2)[1])
    node2.flush()

    node1.close()
    node2.close()

    print("== verify traces")
    for name in ("node1", "node2"):
        violations = verify_trace(str(RUN / name / "trace.jsonl"))
        print(f"  {name}: {'OK' if not violations else violations}")
        if violations:
            return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
