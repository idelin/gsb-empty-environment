"""Spill: memory cap overflow to disk, failure injection, bounded capacity."""
import tempfile
import unittest
from pathlib import Path

from reliable_delivery import (
    CapacityError,
    DeliveryLayer,
    Event,
    PartitionConfig,
    SpillError,
)


def ev(partition, epoch, seq):
    return Event(partition, epoch, seq, f"payload-{partition}-{epoch}-{seq}")


class SpillTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self.tmp.name) / "node"
        self.layer = DeliveryLayer(
            self.dir, PartitionConfig(modulus=256, max_buffered=2, max_spilled=3))

    def tearDown(self):
        self.layer.close()
        self.tmp.cleanup()

    def test_overflow_spills_and_reads_back_in_order(self):
        layer = self.layer
        layer.ingest(ev("S", 0, 1))
        for seq in (3, 4):          # in-memory buffer (cap 2)
            layer.ingest(ev("S", 0, seq))
        for seq in (5, 6, 7):       # spilled to disk (cap 3)
            layer.ingest(ev("S", 0, seq))
        state = layer.partitions["S"]
        self.assertEqual(len(state.buffer), 2)
        self.assertEqual(set(state.spilled), {5, 6, 7})
        _, delivered = layer.authorize_skip("S", 0, 2, 2)
        self.assertEqual([e.seq for e in delivered], [3, 4, 5, 6, 7])
        self.assertEqual(state.spilled, {})

    def test_spill_failure_is_backpressure_not_loss(self):
        layer = self.layer
        layer.ingest(ev("S", 0, 1))
        layer.ingest(ev("S", 0, 3))
        layer.ingest(ev("S", 0, 4))  # buffer full
        layer.spill.fail_next_writes = 1
        with self.assertRaises(SpillError):
            layer.ingest(ev("S", 0, 5))
        state = layer.partitions["S"]
        self.assertEqual(set(state.buffer), {3, 4})  # unchanged
        self.assertEqual(state.spilled, {})            # nothing half-written
        layer.ingest(ev("S", 0, 5))  # retry succeeds
        self.assertEqual(set(state.spilled), {5})

    def test_capacity_is_bounded(self):
        layer = self.layer
        layer.ingest(ev("S", 0, 1))
        for seq in (3, 4, 5, 6, 7):  # 2 in memory + 3 spilled = cap
            layer.ingest(ev("S", 0, seq))
        with self.assertRaises(CapacityError):
            layer.ingest(ev("S", 0, 8))
        state = layer.partitions["S"]
        self.assertLessEqual(len(state.buffer), 2)
        self.assertLessEqual(len(state.spilled), 3)
        spill_files = list((self.dir / "spill").glob("*.json"))
        self.assertLessEqual(len(spill_files), 3)

    def test_spilled_events_survive_crash(self):
        layer = self.layer
        layer.ingest(ev("S", 0, 1))
        for seq in (3, 4, 5, 6):
            layer.ingest(ev("S", 0, seq))
        layer.flush()
        layer.crash()
        layer.recover()
        _, delivered = layer.authorize_skip("S", 0, 2, 2)
        self.assertEqual([e.seq for e in delivered], [3, 4, 5, 6])


if __name__ == "__main__":
    unittest.main()
