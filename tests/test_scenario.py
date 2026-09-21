"""End-to-end controllable-event scenario.

Arrival order 1, 4, 3 with only 1 acked; an authorized skip of 2 releases
3 and 4; then old-epoch 2, new-epoch events and duplicate acks interleave.
On top of that: spill failure, a crash before acks hit disk, one partition
stuck long-term (others still progress, capacity stays bounded) and a
hot migration carrying gap/proof/ack state. Both traces must verify.
"""
import tempfile
import unittest
from pathlib import Path

from reliable_delivery import (
    CapacityError,
    DeliveryLayer,
    Event,
    MigratedAwayError,
    PartitionConfig,
    SpillError,
)
from reliable_delivery.proofs import LATE_EVENT_AFTER_SKIP
from tools.verify_trace import verify_trace


def ev(partition, epoch, seq):
    return Event(partition, epoch, seq, f"payload-{partition}-{epoch}-{seq}")


class ScenarioTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir1 = Path(self.tmp.name) / "node1"
        self.dir2 = Path(self.tmp.name) / "node2"
        config = PartitionConfig(modulus=256, max_buffered=4, max_spilled=4)
        self.layer = DeliveryLayer(self.dir1, config)

    def tearDown(self):
        if self.layer is not None:
            self.layer.close()
        self.tmp.cleanup()

    def test_full_scenario(self):
        layer = self.layer

        # --- out-of-order arrival 1, 4, 3: gap at 2 blocks 3 and 4
        self.assertEqual([e.seq for e in layer.ingest(ev("A", 0, 1))], [1])
        self.assertEqual(layer.ingest(ev("A", 0, 4)), [])
        self.assertEqual(layer.ingest(ev("A", 0, 3)), [])
        self.assertEqual(layer.deliver_position("A"), 2)

        # --- only 1 is acked; duplicate ack is an idempotent no-op
        self.assertTrue(layer.ack("A#0:0:1"))
        self.assertFalse(layer.ack("A#0:0:1"))
        self.assertEqual(layer.ack_position("A"), 2)
        self.assertEqual(layer.deliver_position("A"), 2)  # positions are separate

        # --- authorized skip of 2 releases 3 and 4
        proof, delivered = layer.authorize_skip("A", 0, 2, 2)
        self.assertEqual([e.seq for e in delivered], [3, 4])
        self.assertEqual(layer.deliver_position("A"), 5)

        # --- old-epoch 2 arrives after its gap was crossed: explicit conflict
        self.assertEqual(layer.ingest(ev("A", 0, 2)), [])
        conflicts = layer.conflicts("A")
        self.assertEqual(len(conflicts), 1)
        self.assertEqual(conflicts[0].kind, LATE_EVENT_AFTER_SKIP)

        # --- correction flow delivers the quarantined event, stable id
        correction = layer.resolve_conflict("A", conflicts[0].conflict_id, "deliver")
        self.assertEqual(correction.delivery_id, "A#0:0:2")

        # --- epoch change; new-epoch events reorder independently of old state
        self.assertEqual(layer.advance_epoch("A", 1), [])
        self.assertEqual([e.seq for e in layer.ingest(ev("A", 1, 0))], [0])
        self.assertEqual(layer.ingest(ev("A", 1, 2)), [])
        self.assertEqual([e.seq for e in layer.ingest(ev("A", 1, 1))], [1, 2])
        # duplicate ack of old-epoch 1 interleaved here: still a no-op
        self.assertFalse(layer.ack("A#0:0:1"))

        # --- spill failure: backpressure, nothing lost, retry succeeds
        self.assertEqual([e.seq for e in layer.ingest(ev("C", 0, 1))], [1])
        for seq in (3, 4, 5, 6):
            self.assertEqual(layer.ingest(ev("C", 0, seq)), [])
        layer.spill.fail_next_writes = 1
        with self.assertRaises(SpillError):
            layer.ingest(ev("C", 0, 7))
        state_c = layer.partitions["C"]
        self.assertEqual(set(state_c.buffer), {3, 4, 5, 6})  # intact
        self.assertEqual(state_c.spilled, {})
        layer.ingest(ev("C", 0, 7))  # retry spills to disk
        self.assertEqual(set(state_c.spilled), {7})
        # skip 2 releases buffered + spilled events in order
        _, delivered = layer.authorize_skip("C", 0, 2, 2)
        self.assertEqual([e.seq for e in delivered], [3, 4, 5, 6, 7])
        self.assertEqual(state_c.spilled, {})        # spill files consumed

        # --- crash before acks hit disk: unacked redelivered, ids stable
        layer.flush()
        layer.ack("A#0:0:3")
        layer.ack("A#0:0:4")  # both lost: never flushed
        layer.crash()
        redelivered = layer.recover()
        ids = [e.delivery_id for e in redelivered]
        self.assertIn("A#0:0:3", ids)
        self.assertIn("A#0:0:4", ids)
        self.assertIn("A#1:0:2", ids)
        self.assertNotIn("A#0:0:1", ids)  # acked and flushed before the crash
        # redelivery ids are byte-identical to the original delivery ids
        self.assertEqual(ids, [e.delivery_id for e in redelivered])
        for event in redelivered:
            layer.ack(event.delivery_id)
        layer.flush()
        self.assertEqual(layer.recover(), [])  # nothing left unacked

        # --- stuck partition: no proof for gap 2, capacity stays bounded,
        #     other partitions keep progressing
        self.assertEqual([e.seq for e in layer.ingest(ev("D", 0, 1))], [1])
        layer.ack("D#0:0:1")
        for seq in (3, 4, 5, 6):      # fills the in-memory buffer
            self.assertEqual(layer.ingest(ev("D", 0, seq)), [])
        for seq in (10, 11, 12, 13):  # overflows to spill
            self.assertEqual(layer.ingest(ev("D", 0, seq)), [])
        with self.assertRaises(CapacityError):
            layer.ingest(ev("D", 0, 14))
        state_d = layer.partitions["D"]
        self.assertLessEqual(len(state_d.buffer), 4)
        self.assertLessEqual(len(state_d.spilled), 4)
        self.assertEqual(layer.deliver_position("D"), 2)  # still stuck
        self.assertEqual([e.seq for e in layer.ingest(ev("A", 1, 3))], [3])

        # --- hot migration of D carries gap, proofs and ack state
        layer.authorize_skip("D", 0, 7, 9)  # uncrossed proof for a later gap
        bundle = layer.export_partition("D")
        with self.assertRaises(MigratedAwayError):
            layer.ingest(ev("D", 0, 2))

        layer2 = DeliveryLayer(self.dir2, layer.config)
        try:
            layer2.import_partition(bundle)
            state_d2 = layer2.partitions["D"]
            self.assertEqual(state_d2.deliver_next, 2)          # gap carried
            self.assertEqual(state_d2.acked, {"D#0:0:1"})         # acks carried
            self.assertEqual(state_d2.ack_next, 2)
            self.assertEqual(len(state_d2.proofs), 1)           # proofs carried
            self.assertEqual(set(state_d2.buffer) | set(state_d2.spilled),
                             {3, 4, 5, 6, 10, 11, 12, 13})
            # authorizing the carried gap on the new home resumes delivery,
            # and the carried proof for [7,9] is honored across the move
            _, delivered = layer2.authorize_skip("D", 0, 2, 2)
            self.assertEqual([e.seq for e in delivered],
                             [3, 4, 5, 6, 10, 11, 12, 13])
            layer2.flush()
        finally:
            layer2.close()

        # --- both delivery traces must satisfy the delivery contract
        layer.close()
        self.assertEqual(verify_trace(str(self.dir1 / "trace.jsonl")), [])
        self.assertEqual(verify_trace(str(self.dir2 / "trace.jsonl")), [])
        self.layer = None  # already closed


if __name__ == "__main__":
    unittest.main()
