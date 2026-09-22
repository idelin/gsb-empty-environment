import os
import tempfile
import unittest

from watermark_kernel import DataMessage, Decision, ProgressMessage, WatermarkKernel
from watermark_kernel.errors import CheckpointChecksumError, CorruptedLogError
from watermark_kernel.storage import LOG_NAME


def boot(directory, **kwargs):
    kernel = WatermarkKernel.open(directory, **kwargs)
    for source, epoch in [("A", "a"), ("B", "b")]:
        kernel.proposal(source, epoch)
        kernel.accept_source(source, epoch, 0)
    return kernel


class PersistenceTests(unittest.TestCase):
    def test_checkpoint_recovers_gaps_acks_and_unconfirmed_decision(self):
        with tempfile.TemporaryDirectory() as directory:
            decisions = []
            kernel = boot(directory, checkpoint_every=1, publisher=decisions.append)
            kernel.progress(ProgressMessage("A", "a", 10, 1))
            kernel.data(DataMessage("A", "a", 1, 1))
            kernel.ack("A", "a", 1)
            kernel.progress(ProgressMessage("B", "b", 8, 0))
            self.assertEqual(decisions[-1].boundary, 8)

            recovered = WatermarkKernel.open(
                directory,
                checkpoint_every=1,
                publisher=decisions.append,
            )
            self.assertEqual(recovered.boundary, 8)
            self.assertEqual(recovered.unconfirmed_decisions, {"decision-000000000001"})
            self.assertEqual(len(decisions), 2)
            self.assertEqual(decisions[0], decisions[1])
            recovered.ack_decision("decision-000000000001")

            final = WatermarkKernel.open(directory, checkpoint_every=1, publisher=decisions.append)
            self.assertEqual(final.unconfirmed_decisions, set())
            self.assertEqual(len(decisions), 2)

    def test_torn_tail_is_truncated_and_replay_remains_stable(self):
        with tempfile.TemporaryDirectory() as directory:
            kernel = boot(directory, checkpoint_every=100)
            kernel.progress(ProgressMessage("A", "a", 10, 0))
            kernel.mark_idle("B")
            path = os.path.join(directory, LOG_NAME)
            with open(path, "ab") as stream:
                stream.write(b"WLE1\teyJ0IjoicGFydGlhbCJ9\t00000000\n")
                stream.flush()
                os.fsync(stream.fileno())
            recovered = WatermarkKernel.open(directory, checkpoint_every=100)
            self.assertEqual(recovered.boundary, 10)

    def test_middle_corruption_is_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            kernel = boot(directory, checkpoint_every=100)
            kernel.progress(ProgressMessage("A", "a", 10, 0))
            first_size = os.path.getsize(os.path.join(directory, LOG_NAME))
            kernel.progress(ProgressMessage("B", "b", 8, 0))
            path = os.path.join(directory, LOG_NAME)
            with open(path, "r+b") as stream:
                stream.seek(first_size)
                stream.write(b"X")
                stream.flush()
                os.fsync(stream.fileno())
            with self.assertRaises(CorruptedLogError):
                WatermarkKernel.open(directory, checkpoint_every=100)

    def test_checksum_checkpoint_rejects_corrupt_snapshot(self):
        with tempfile.TemporaryDirectory() as directory:
            kernel = boot(directory, checkpoint_every=1)
            kernel.progress(ProgressMessage("A", "a", 10, 0))
            path = os.path.join(directory, "checkpoint.json")
            with open(path, "r+b") as stream:
                stream.seek(20)
                stream.write(b"X")
                stream.flush()
                os.fsync(stream.fileno())
            with self.assertRaises(CheckpointChecksumError):
                WatermarkKernel.open(directory, checkpoint_every=1)


if __name__ == "__main__":
    unittest.main()
