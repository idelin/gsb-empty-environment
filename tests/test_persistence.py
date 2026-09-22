"""Persistence: journal framing, torn-tail repair, corruption policy,
checkpoints, and stable decision IDs across restarts."""
import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from wmcoord import (
    Ack,
    CorruptLogError,
    CrashNow,
    Data,
    DataMessage,
    FileLog,
    Kernel,
    MemoryLog,
    Progress,
    ProgressMessage,
    RegisterSource,
)


def progress(frontier, cut=0):
    return Progress(ProgressMessage("A", 1, frontier, cut))


class TestJournalRecovery(unittest.TestCase):
    def test_torn_tail_is_truncated(self):
        log = MemoryLog()
        k = Kernel.open(log)
        k.submit(RegisterSource("A"))
        k.submit(progress(50))
        log._buf.extend(b"\x00\x00\x00\x0apartial")  # half-written frame
        k2 = Kernel.open(log)
        self.assertEqual(k2.boundary, 50)
        k2.submit(progress(60))  # log still appendable and consistent
        self.assertEqual(k2.boundary, 60)
        k3 = Kernel.open(log)
        self.assertEqual(k3.boundary, 60)

    def test_torn_tail_zeros(self):
        log = MemoryLog()
        k = Kernel.open(log)
        k.submit(RegisterSource("A"))
        k.submit(progress(50))
        log._buf.extend(b"\x00" * 13)
        k2 = Kernel.open(log)
        self.assertEqual(k2.boundary, 50)

    def test_middle_corruption_refuses_to_open(self):
        log = MemoryLog()
        k = Kernel.open(log)
        k.submit(RegisterSource("A"))
        k.submit(progress(50))
        k.submit(progress(60))
        log._buf[len(b"WMLGv1\n") + 10] ^= 0xFF  # corrupt first record
        with self.assertRaises(CorruptLogError):
            Kernel.open(log)

    def test_bad_magic_refuses_to_open(self):
        log = MemoryLog()
        log._buf[:7] = b"XXXXXXX"
        with self.assertRaises(CorruptLogError):
            Kernel.open(log)

    def test_decision_ids_stable_across_restart(self):
        log = MemoryLog()
        k = Kernel.open(log)
        k.submit(RegisterSource("A"))
        k.submit(progress(50))
        first = k.pending_decisions()
        k2 = Kernel.open(log)
        self.assertEqual(k2.pending_decisions(), first)
        k2.submit(progress(60))
        k3 = Kernel.open(log)
        self.assertEqual(
            [d[0] for d in k3.pending_decisions()],
            ["D00000001", "D00000002"],
        )

    def test_crash_between_command_and_decision_is_repaired(self):
        log = MemoryLog()
        armed = {"on": False}

        def hook(point):
            if point == "after_command_write" and armed["on"]:
                raise CrashNow()

        k = Kernel.open(log, crash_hook=hook)
        k.submit(RegisterSource("A"))
        armed["on"] = True
        with self.assertRaises(CrashNow):
            k.submit(progress(50))
        k2 = Kernel.open(log)
        self.assertEqual(k2.boundary, 50)
        self.assertEqual([d[0] for d in k2.pending_decisions()], ["D00000001"])

    def test_publish_confirm_survive_restart(self):
        log = MemoryLog()
        k = Kernel.open(log)
        k.submit(RegisterSource("A"))
        k.submit(progress(50))
        did, boundary, _ = k.pending_decisions()[0]
        k.publish(did, boundary)
        k2 = Kernel.open(log)
        self.assertEqual(k2.pending_decisions()[0][2], "published")
        k2.confirm(did)
        k3 = Kernel.open(log)
        self.assertEqual(k3.pending_decisions(), [])


class TestCheckpoints(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.cp = os.path.join(self.tmp, "ckpt.bin")

    def test_checkpoint_roundtrip_with_gaps_and_acks(self):
        log = MemoryLog()
        k = Kernel.open(log, checkpoint_path=self.cp)
        k.submit(RegisterSource("A"))
        k.submit(progress(100, cut=3))
        k.submit(Data(DataMessage("A", 1, 1, 10)))
        k.submit(Data(DataMessage("A", 1, 3, 30)))
        k.submit(Ack("A", 1, 1))
        k.save_checkpoint()
        k2 = Kernel.open(log, checkpoint_path=self.cp)
        # Gap at seq 2 and unacked seq 3 must still block after recovery.
        self.assertEqual(k2.boundary, 0)
        k2.submit(Data(DataMessage("A", 1, 2, 20)))
        self.assertEqual(k2.boundary, 0)
        k2.submit(Ack("A", 1, 2))
        k2.submit(Ack("A", 1, 3))
        self.assertEqual(k2.boundary, 100)

    def test_corrupt_checkpoint_falls_back_to_journal(self):
        log = MemoryLog()
        k = Kernel.open(log, checkpoint_path=self.cp)
        k.submit(RegisterSource("A"))
        k.submit(progress(50))
        k.save_checkpoint()
        k.submit(progress(60))
        with open(self.cp, "r+b") as fh:
            fh.seek(6)
            fh.write(b"\xAA")
        k2 = Kernel.open(log, checkpoint_path=self.cp)
        self.assertEqual(k2.boundary, 60)

    def test_file_log_end_to_end(self):
        path = os.path.join(self.tmp, "journal.log")
        log = FileLog(path)
        k = Kernel.open(log, checkpoint_path=self.cp)
        k.submit(RegisterSource("A"))
        k.submit(progress(50))
        k.save_checkpoint()
        log.close()
        k2 = Kernel.open(FileLog(path), checkpoint_path=self.cp)
        self.assertEqual(k2.boundary, 50)
        k2.submit(progress(70))
        self.assertEqual(k2.boundary, 70)

    def test_file_log_torn_tail(self):
        path = os.path.join(self.tmp, "journal.log")
        log = FileLog(path)
        k = Kernel.open(log)
        k.submit(RegisterSource("A"))
        k.submit(progress(50))
        log.close()
        with open(path, "ab") as fh:
            fh.write(b"\x00\x00\x00\x20truncated")
        k2 = Kernel.open(FileLog(path))
        self.assertEqual(k2.boundary, 50)


if __name__ == "__main__":
    unittest.main()


class TestFreshHandleRecovery(unittest.TestCase):
    """A brand-new log object on an existing file must continue transaction
    numbering; otherwise the next recovery would see colliding txns."""

    def setUp(self):
        self.tmp = tempfile.mkdtemp()
        self.path = os.path.join(self.tmp, "journal.log")

    def test_three_generations_of_handles(self):
        log1 = FileLog(self.path)
        k1 = Kernel.open(log1)
        k1.submit(RegisterSource("A"))
        k1.submit(progress(50))
        log1.close()

        k2 = Kernel.open(FileLog(self.path))
        self.assertEqual(k2.boundary, 50)
        k2.submit(progress(60))
        k2.submit(progress(70))

        k3 = Kernel.open(FileLog(self.path))
        self.assertEqual(k3.boundary, 70)
        self.assertEqual(
            [d[0] for d in k3.pending_decisions()],
            ["D00000001", "D00000002", "D00000003"],
        )
