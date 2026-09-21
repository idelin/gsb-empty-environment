"""Skip-proof lifecycle: revoke before cross, conflict after cross."""
import tempfile
import unittest
from pathlib import Path

from reliable_delivery import DeliveryLayer, Event, PartitionConfig
from reliable_delivery.proofs import LATE_EVENT_AFTER_SKIP, REVOKE_AFTER_CROSS


def ev(partition, epoch, seq):
    return Event(partition, epoch, seq, f"payload-{partition}-{epoch}-{seq}")


class ProofTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self.tmp.name) / "node"
        self.layer = DeliveryLayer(self.dir, PartitionConfig(modulus=256))

    def tearDown(self):
        self.layer.close()
        self.tmp.cleanup()

    def test_revoke_before_cross_cancels_the_proof(self):
        layer = self.layer
        layer.ingest(ev("R", 0, 1))
        proof, _ = layer.authorize_skip("R", 0, 5, 6)  # future gap, uncrossed
        self.assertTrue(layer.revoke_skip("R", proof.proof_id))
        # cross a different gap to move the cursor past the revoked range
        layer.authorize_skip("R", 0, 2, 4)
        self.assertEqual(layer.deliver_position("R"), 5)
        # revoked proof must not fire: 7 waits for the real 5 and 6
        self.assertEqual(layer.ingest(ev("R", 0, 7)), [])
        self.assertEqual(layer.deliver_position("R"), 5)

    def test_revoke_after_cross_is_an_explicit_conflict(self):
        layer = self.layer
        layer.ingest(ev("R", 0, 1))
        layer.ingest(ev("R", 0, 3))
        proof, delivered = layer.authorize_skip("R", 0, 2, 2)
        self.assertEqual([e.seq for e in delivered], [3])  # gap crossed
        self.assertFalse(layer.revoke_skip("R", proof.proof_id))
        conflicts = layer.conflicts("R")
        self.assertEqual(len(conflicts), 1)
        self.assertEqual(conflicts[0].kind, REVOKE_AFTER_CROSS)
        layer.resolve_conflict("R", conflicts[0].conflict_id, "drop")
        self.assertTrue(layer.conflicts("R")[0].resolved)

    def test_late_original_after_cross_enters_correction_flow(self):
        layer = self.layer
        layer.ingest(ev("R", 0, 1))
        layer.ingest(ev("R", 0, 3))
        layer.authorize_skip("R", 0, 2, 2)
        self.assertEqual(layer.ingest(ev("R", 0, 2)), [])  # too late
        conflict = layer.conflicts("R")[0]
        self.assertEqual(conflict.kind, LATE_EVENT_AFTER_SKIP)
        # correction: drop -> event stays quarantined, never delivered
        self.assertIsNone(layer.resolve_conflict("R", conflict.conflict_id, "drop"))
        self.assertEqual(layer.deliver_position("R"), 4)

    def test_stale_epoch_event_is_a_conflict_not_a_delivery(self):
        layer = self.layer
        layer.ingest(ev("R", 0, 1))
        layer.advance_epoch("R", 1)
        self.assertEqual(layer.ingest(ev("R", 0, 2)), [])
        self.assertEqual(len(layer.conflicts("R")), 1)

    def test_future_epoch_event_is_held_until_epoch_advances(self):
        layer = self.layer
        layer.ingest(ev("R", 0, 1))
        self.assertEqual(layer.ingest(ev("R", 1, 0)), [])  # early arrival
        delivered = layer.advance_epoch("R", 1)
        self.assertEqual([e.seq for e in delivered], [0])


if __name__ == "__main__":
    unittest.main()
