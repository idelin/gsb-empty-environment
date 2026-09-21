"""Hot migration: gaps, proofs and ack state move with the partition."""
import tempfile
import unittest
from pathlib import Path

from reliable_delivery import DeliveryLayer, Event, MigratedAwayError, PartitionConfig
from reliable_delivery.proofs import LATE_EVENT_AFTER_SKIP
from tools.verify_trace import verify_trace


def ev(partition, epoch, seq):
    return Event(partition, epoch, seq, f"payload-{partition}-{epoch}-{seq}")


class MigrationTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        root = Path(self.tmp.name)
        config = PartitionConfig(modulus=256, max_buffered=4, max_spilled=8)
        self.src = DeliveryLayer(root / "src", config)
        self.dst = DeliveryLayer(root / "dst", config)

    def tearDown(self):
        self.src.close()
        self.dst.close()
        self.tmp.cleanup()

    def test_state_carried_and_delivery_resumes(self):
        src, dst = self.src, self.dst
        src.ingest(ev("M", 0, 1))
        src.ack("M#0:0:1")
        src.ingest(ev("M", 0, 3))
        src.ingest(ev("M", 0, 4))
        src.authorize_skip("M", 0, 7, 8)  # uncrossed proof for a later gap
        src.ingest(ev("M", 0, 9))
        src.flush()

        bundle = src.export_partition("M")
        dst.import_partition(bundle)
        state = dst.partitions["M"]
        self.assertEqual(state.deliver_next, 2)           # gap carried
        self.assertEqual(state.acked, {"M#0:0:1"})          # ack state carried
        self.assertIn("M-sk0", state.proofs)              # proof carried
        self.assertEqual(set(state.buffer), {3, 4, 9})

        # source is tombstoned
        with self.assertRaises(MigratedAwayError):
            src.ingest(ev("M", 0, 2))
        with self.assertRaises(MigratedAwayError):
            src.ack("M#0:0:3")

        # target resumes exactly where the source stopped
        _, delivered = dst.authorize_skip("M", 0, 2, 2)
        self.assertEqual([e.seq for e in delivered], [3, 4])  # 9 waits on 5..8
        self.assertEqual([e.seq for e in dst.ingest(ev("M", 0, 5))], [5])
        # the carried proof for [7,8] fires on the new home, releasing 9
        self.assertEqual([e.seq for e in dst.ingest(ev("M", 0, 6))], [6, 9])
        self.assertEqual(dst.deliver_position("M"), 10)

    def test_target_recovers_from_snapshot_after_crash(self):
        src, dst = self.src, self.dst
        src.ingest(ev("M", 0, 1))
        src.ingest(ev("M", 0, 3))
        src.flush()
        dst.import_partition(src.export_partition("M"))
        dst.ack("M#0:0:1")  # carried ack plus new activity
        dst.flush()
        dst.crash()
        redelivered = dst.recover()  # replays the snapshot record
        self.assertEqual([e.delivery_id for e in redelivered], [])
        state = dst.partitions["M"]
        self.assertEqual(state.deliver_next, 2)
        self.assertEqual(set(state.buffer), {3})

    def test_traces_verify_on_both_sides(self):
        src, dst = self.src, self.dst
        src.ingest(ev("M", 0, 1))
        src.ack("M#0:0:1")
        src.ingest(ev("M", 0, 3))
        src.flush()
        dst.import_partition(src.export_partition("M"))
        dst.authorize_skip("M", 0, 2, 2)
        dst.flush()
        src.close()
        dst.close()
        self.assertEqual(verify_trace(str(src.workdir / "trace.jsonl")), [])
        self.assertEqual(verify_trace(str(dst.workdir / "trace.jsonl")), [])


if __name__ == "__main__":
    unittest.main()
