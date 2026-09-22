import tempfile
import unittest

from watermark_kernel import DataMessage, ProgressMessage, WatermarkKernel
from watermark_kernel.errors import InvalidStateError, StaleEpochError


class ScenarioTests(unittest.TestCase):
    def make_kernel(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        return WatermarkKernel.open(self.temp.name, checkpoint_every=1)

    def add_sources(self, kernel, *sources):
        for source_id, epoch in sources:
            kernel.proposal(source_id, epoch)
            kernel.accept_source(source_id, epoch, 0)

    def test_gap_and_explicit_ack_guard_boundary(self):
        kernel = self.make_kernel()
        self.add_sources(kernel, ("A", "a"), ("B", "b"))
        kernel.progress(ProgressMessage("A", "a", 100, 3))
        kernel.data(DataMessage("A", "a", 1, 1))
        kernel.data(DataMessage("A", "a", 3, 3))
        kernel.progress(ProgressMessage("B", "b", 80, 0))
        self.assertEqual(kernel.boundary, 0)
        self.assertEqual(kernel.pending_gap("A"), [2])
        kernel.data(DataMessage("A", "a", 2, 2))
        self.assertEqual(kernel.boundary, 0)
        for seq in (1, 2, 3):
            kernel.ack("A", "a", seq)
        self.assertEqual(kernel.boundary, 80)
        kernel.mark_idle("B")
        self.assertEqual(kernel.boundary, 100)

    def test_idle_timeout_keeps_unacknowledged_block(self):
        kernel = self.make_kernel()
        self.add_sources(kernel, ("A", "a"), ("B", "b"))
        kernel.progress(ProgressMessage("A", "a", 100, 90))
        for seq in range(1, 91):
            kernel.data(DataMessage("A", "a", seq, seq))
        for seq in range(1, 90):
            kernel.ack("A", "a", seq)
        kernel.progress(ProgressMessage("B", "b", 95, 0))
        kernel.mark_idle("A")
        self.assertEqual(kernel.boundary, 0)
        kernel.ack("A", "a", 90)
        self.assertEqual(kernel.boundary, 95)

    def test_handover_is_atomic_and_rejects_old_parent(self):
        kernel = self.make_kernel()
        self.add_sources(kernel, ("P", "p"))
        kernel.progress(ProgressMessage("P", "p", 100, 2))
        for seq in (1, 2):
            kernel.data(DataMessage("P", "p", seq, seq))
            kernel.ack("P", "p", seq)
        kernel.begin_handover("split-1", "P", "p", 100, 2, [("C1", "c1"), ("C2", "c2")])
        kernel.confirm_child("split-1", "C1", "c1")
        self.assertEqual(set(kernel.sources), {"P"})
        self.assertEqual(kernel.handovers["split-1"].status, "pending")
        kernel.confirm_child("split-1", "C2", "c2")
        self.assertEqual(set(kernel.sources), {"C1", "C2"})
        self.assertEqual(kernel.handovers["split-1"].status, "committed")
        with self.assertRaises(StaleEpochError):
            kernel.data(DataMessage("P", "p", 3, 101))

    def test_late_reactivation_does_not_change_closed_result(self):
        kernel = self.make_kernel()
        self.add_sources(kernel, ("A", "a"))
        kernel.progress(ProgressMessage("A", "a", 10, 0))
        kernel.mark_idle("A")
        self.assertEqual(kernel.boundary, 10)
        result = kernel.data(DataMessage("A", "a", 1, 5))
        self.assertEqual(result.kind, "late")
        self.assertEqual(kernel.boundary, 10)
        self.assertEqual(kernel.late_events("A")[0]["event_time"], 5)

    def test_all_idle_sources_retain_previous_boundary(self):
        kernel = self.make_kernel()
        self.add_sources(kernel, ("A", "a"), ("B", "b"))
        kernel.progress(ProgressMessage("A", "a", 10, 0))
        kernel.progress(ProgressMessage("B", "b", 10, 0))
        kernel.mark_idle("A")
        kernel.mark_idle("B")
        self.assertEqual(kernel.boundary, 10)

    def test_pending_handover_can_be_cancelled_before_completion(self):
        kernel = self.make_kernel()
        self.add_sources(kernel, ("P", "p"))
        kernel.progress(ProgressMessage("P", "p", 10, 0))
        kernel.begin_handover("split", "P", "p", 10, 0, [("C1", "c1"), ("C2", "c2")])
        kernel.cancel_handover("split")
        self.assertEqual(set(kernel.sources), {"P"})
        with self.assertRaises(InvalidStateError):
            kernel.cancel_handover("split")


if __name__ == "__main__":
    unittest.main()
