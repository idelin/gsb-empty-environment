"""Sequence-number wraparound on a small ring (modulus 8)."""
import tempfile
import unittest
from pathlib import Path

from reliable_delivery import DeliveryLayer, Event, PartitionConfig
from tools.verify_trace import verify_trace


def ev(partition, epoch, seq):
    return Event(partition, epoch, seq, f"payload-{partition}-{epoch}-{seq}")


class WraparoundTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self.tmp.name) / "node"
        self.layer = DeliveryLayer(self.dir, PartitionConfig(modulus=8))

    def tearDown(self):
        self.layer.close()
        self.tmp.cleanup()

    def test_delivery_continues_across_wrap(self):
        layer = self.layer
        for seq in range(8):
            self.assertEqual([e.seq for e in layer.ingest(ev("W", 0, seq))], [seq])
        self.assertEqual(layer.deliver_position("W"), 0)  # wrapped
        # reorder across the boundary: 1 arrives before 0
        self.assertEqual(layer.ingest(ev("W", 0, 1)), [])
        self.assertEqual([e.seq for e in layer.ingest(ev("W", 0, 0))], [0, 1])

    def test_skip_across_wrap_boundary(self):
        layer = self.layer
        for seq in range(8):
            layer.ingest(ev("W", 0, seq))
        layer.ingest(ev("W", 0, 0))
        layer.ingest(ev("W", 0, 3))  # gap at 1,2 past the wrap
        self.assertEqual(layer.deliver_position("W"), 1)
        _, delivered = layer.authorize_skip("W", 0, 1, 2)
        self.assertEqual([e.seq for e in delivered], [3])

    def test_duplicate_from_previous_cycle_is_ignored(self):
        layer = self.layer
        for seq in range(8):
            layer.ingest(ev("W", 0, seq))
        layer.ingest(ev("W", 0, 0))
        self.assertEqual(layer.ingest(ev("W", 0, 7)), [])  # old cycle dup
        self.assertEqual(layer.deliver_position("W"), 1)

    def test_trace_verifies_with_wraparound(self):
        layer = self.layer
        for seq in range(8):
            layer.ingest(ev("W", 0, seq))
        layer.ingest(ev("W", 0, 1))
        layer.ingest(ev("W", 0, 0))
        layer.ingest(ev("W", 0, 4))
        layer.authorize_skip("W", 0, 2, 3)
        layer.close()
        self.assertEqual(verify_trace(str(self.dir / "trace.jsonl")), [])


if __name__ == "__main__":
    unittest.main()
