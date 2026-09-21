"""Recovery as controllable events: crash, replay, redeliver."""
import tempfile
import unittest
from pathlib import Path

from reliable_delivery import DeliveryLayer, Event, PartitionConfig
from tools.verify_trace import verify_trace


def ev(partition, epoch, seq):
    return Event(partition, epoch, seq, f"payload-{partition}-{epoch}-{seq}")


class RecoveryTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self.tmp.name) / "node"
        self.layer = DeliveryLayer(self.dir, PartitionConfig(modulus=256))

    def tearDown(self):
        self.layer.close()
        self.tmp.cleanup()

    def test_unacked_redelivered_with_stable_ids(self):
        layer = self.layer
        delivered = layer.ingest(ev("P", 0, 1)) + layer.ingest(ev("P", 0, 2))
        layer.flush()
        original_ids = [e.delivery_id for e in delivered]
        layer.crash()
        redelivered = layer.recover()
        self.assertEqual([e.delivery_id for e in redelivered], original_ids)
        self.assertEqual([e.delivery_id for e in redelivered],
                         ["P#0:0:1", "P#0:0:2"])

    def test_crash_before_ack_persisted(self):
        layer = self.layer
        layer.ingest(ev("P", 0, 1))
        layer.ingest(ev("P", 0, 2))
        layer.flush()
        layer.ack("P#0:0:1")   # applied downstream, never flushed
        layer.crash()
        ids = [e.delivery_id for e in layer.recover()]
        self.assertEqual(ids, ["P#0:0:1", "P#0:0:2"])  # 1 comes back too
        # duplicate downstream ack after recovery stays idempotent
        self.assertTrue(layer.ack("P#0:0:1"))
        self.assertFalse(layer.ack("P#0:0:1"))

    def test_unflushed_ingest_is_lost_and_source_resends(self):
        layer = self.layer
        layer.ingest(ev("P", 0, 1))
        layer.flush()
        layer.ingest(ev("P", 0, 2))  # not flushed -> lost in the crash
        layer.crash()
        self.assertEqual([e.delivery_id for e in layer.recover()], ["P#0:0:1"])
        # the source redelivers 2; the layer accepts it as new
        self.assertEqual([e.seq for e in layer.ingest(ev("P", 0, 2))], [2])

    def test_proofs_and_conflicts_survive_recovery(self):
        layer = self.layer
        layer.ingest(ev("P", 0, 1))
        layer.ingest(ev("P", 0, 3))
        layer.authorize_skip("P", 0, 2, 2)
        layer.ingest(ev("P", 0, 2))  # late original -> conflict
        layer.flush()
        layer.crash()
        layer.recover()
        state = layer.partitions["P"]
        self.assertEqual(state.deliver_next, 4)
        self.assertTrue(state.proofs["P-sk0"].crossed)
        self.assertEqual(len(state.conflicts), 1)
        self.assertEqual(state.quarantine[0].seq, 2)

    def test_trace_verifies_across_crash(self):
        layer = self.layer
        layer.ingest(ev("P", 0, 1))
        layer.ingest(ev("P", 0, 2))
        layer.flush()
        layer.ack("P#0:0:1")
        layer.crash()
        for event in layer.recover():
            layer.ack(event.delivery_id)
        layer.flush()
        layer.close()
        self.assertEqual(verify_trace(str(self.dir / "trace.jsonl")), [])


if __name__ == "__main__":
    unittest.main()
