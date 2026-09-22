"""Canonical scenarios from the coordination spec."""
import os
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from wmcoord import (
    Ack,
    BeginHandoff,
    CancelHandoff,
    ChildTakeover,
    Data,
    DataMessage,
    IdleTimeout,
    JoinSource,
    Kernel,
    MemoryLog,
    Progress,
    ProgressMessage,
    ProtocolError,
    RegisterSource,
)


def kernel():
    return Kernel.open(MemoryLog())


class TestGapAndAck(unittest.TestCase):
    """Scenario 1: gap blocks, missing ack blocks, then 80, then idle -> 100."""

    def test_scenario_1(self):
        k = kernel()
        self.assertEqual(k.boundary, 0)  # initial boundary is 0
        k.submit(RegisterSource("A"))
        k.submit(RegisterSource("B"))
        k.submit(Progress(ProgressMessage("A", 1, 100, 3)))
        k.submit(Data(DataMessage("A", 1, 1, 10)))
        k.submit(Data(DataMessage("A", 1, 3, 30)))
        k.submit(Progress(ProgressMessage("B", 1, 80, 0)))
        # B has effective water 80, but A has a gap (seq 2) inside cut=3.
        self.assertEqual(k.boundary, 0)
        # Gap filled but not yet processed/acknowledged: still blocked.
        k.submit(Data(DataMessage("A", 1, 2, 20)))
        self.assertEqual(k.boundary, 0)
        # All three confirmed: A's water reaches 100; global = min(100, 80).
        for seq in (1, 2, 3):
            k.submit(Ack("A", 1, seq))
        self.assertEqual(k.boundary, 80)
        # B goes idle with nothing pending: it leaves the minimum.
        k.submit(IdleTimeout("B"))
        self.assertEqual(k.boundary, 100)

    def test_scenario_2_unacked_blocks_idle(self):
        """Unacked t=90 event blocks closure even after the source idles."""
        k = kernel()
        k.submit(RegisterSource("A"))
        k.submit(Progress(ProgressMessage("A", 1, 100, 1)))
        k.submit(Data(DataMessage("A", 1, 1, 90)))
        k.submit(IdleTimeout("A"))  # timeout must not clear the blocker
        self.assertEqual(k.boundary, 0)
        k.submit(Ack("A", 1, 1))
        self.assertEqual(k.boundary, 100)

    def test_idle_with_gap_still_blocks(self):
        k = kernel()
        k.submit(RegisterSource("A"))
        k.submit(Progress(ProgressMessage("A", 1, 100, 2)))
        k.submit(Data(DataMessage("A", 1, 1, 10)))
        k.submit(Ack("A", 1, 1))
        k.submit(IdleTimeout("A"))  # gap at seq 2 inside cut: still blocks
        self.assertEqual(k.boundary, 0)

    def test_all_idle_retains_last_boundary(self):
        k = kernel()
        k.submit(RegisterSource("A"))
        k.submit(Progress(ProgressMessage("A", 1, 50, 0)))
        self.assertEqual(k.boundary, 50)
        k.submit(IdleTimeout("A"))
        # No participants: boundary retained, never jumps to infinity.
        self.assertEqual(k.boundary, 50)

    def test_progress_before_data(self):
        k = kernel()
        k.submit(RegisterSource("A"))
        k.submit(Progress(ProgressMessage("A", 1, 100, 2)))  # progress first
        k.submit(Data(DataMessage("A", 1, 2, 20)))           # out of order
        k.submit(Data(DataMessage("A", 1, 1, 10)))
        k.submit(Ack("A", 1, 2))                             # ack out of order
        k.submit(Ack("A", 1, 1))
        self.assertEqual(k.boundary, 100)

    def test_duplicate_cannot_skip_gap(self):
        k = kernel()
        k.submit(RegisterSource("A"))
        k.submit(Progress(ProgressMessage("A", 1, 100, 2)))
        k.submit(Data(DataMessage("A", 1, 2, 20)))
        r = k.submit(Data(DataMessage("A", 1, 2, 20)))  # duplicate
        self.assertEqual(r.disposition, "ignored")
        k.submit(Ack("A", 1, 2))
        self.assertEqual(k.boundary, 0)  # seq 1 still missing

    def test_conflicting_duplicate_rejected(self):
        k = kernel()
        k.submit(RegisterSource("A"))
        k.submit(Data(DataMessage("A", 1, 1, 10)))
        with self.assertRaises(ProtocolError):
            k.submit(Data(DataMessage("A", 1, 1, 99)))

    def test_epoch_no_reuse(self):
        k = kernel()
        k.submit(RegisterSource("A"))
        k.submit(Progress(ProgressMessage("A", 1, 50, 0)))
        k.submit(Progress(ProgressMessage("A", 2, 60, 0)))
        # Old-epoch progress is stale and ignored; old-epoch data is late.
        r = k.submit(Progress(ProgressMessage("A", 1, 70, 0)))
        self.assertEqual(r.disposition, "ignored")
        r = k.submit(Data(DataMessage("A", 1, 1, 10)))
        self.assertEqual(r.disposition, "late")
        # Reusing the current epoch with a regressive frontier is an error.
        with self.assertRaises(ProtocolError):
            k.submit(Progress(ProgressMessage("A", 2, 40, 0)))
        self.assertEqual(k.boundary, 60)

    def test_new_source_must_accept_floor(self):
        k = kernel()
        k.submit(RegisterSource("A"))
        k.submit(Progress(ProgressMessage("A", 1, 50, 0)))
        self.assertEqual(k.boundary, 50)
        with self.assertRaises(ProtocolError):
            k.submit(JoinSource("B", 0))  # stale floor rejected
        k.submit(JoinSource("B", 50))
        k.submit(Progress(ProgressMessage("B", 1, 70, 0)))
        self.assertEqual(k.boundary, 50)  # A still at 50

    def test_late_data_goes_to_late_channel(self):
        k = kernel()
        k.submit(RegisterSource("A"))
        k.submit(Progress(ProgressMessage("A", 1, 50, 0)))
        self.assertEqual(k.boundary, 50)
        r = k.submit(Data(DataMessage("A", 1, 1, 10)))  # ts below closed 50
        self.assertEqual(r.disposition, "late")
        self.assertEqual(len(k.state["late"]), 1)
        self.assertEqual(k.boundary, 50)  # closed results unchanged


class TestHandoff(unittest.TestCase):
    """Scenario 3: parent splits into two children at H=100."""

    def _ready_parent(self, k):
        k.submit(RegisterSource("P"))
        k.submit(Progress(ProgressMessage("P", 1, 100, 2)))
        k.submit(Data(DataMessage("P", 1, 1, 40)))
        k.submit(Data(DataMessage("P", 1, 2, 60)))
        k.submit(Ack("P", 1, 1))
        k.submit(Ack("P", 1, 2))
        self.assertEqual(k.boundary, 100)

    def test_handoff_requires_both_children(self):
        k = kernel()
        self._ready_parent(k)
        k.submit(BeginHandoff("P", "h1", 100, children=("C1", "C2")))
        k.submit(ChildTakeover("h1", "C1", 1, 100))
        # Only one child confirmed: handoff not complete, parent still in.
        self.assertEqual(k.state["handoffs"]["h1"]["status"], "pending")
        self.assertEqual(k.state["sources"]["P"]["status"], "active")
        r = k.submit(ChildTakeover("h1", "C2", 1, 100))
        self.assertEqual(r.handoff_completed, "h1")
        self.assertEqual(k.state["sources"]["P"]["status"], "exited")
        # Old parent messages can no longer move anything.
        r = k.submit(Data(DataMessage("P", 1, 3, 50)))
        self.assertEqual(r.disposition, "late")
        r = k.submit(Progress(ProgressMessage("P", 1, 500, 3)))
        self.assertEqual(r.disposition, "ignored")
        self.assertEqual(k.boundary, 100)
        # Children participate from H=100.
        k.submit(Progress(ProgressMessage("C1", 1, 150, 0)))
        k.submit(Progress(ProgressMessage("C2", 1, 120, 0)))
        self.assertEqual(k.boundary, 120)

    def test_handoff_boundary_below_global_rejected(self):
        k = kernel()
        self._ready_parent(k)
        with self.assertRaises(ProtocolError):
            k.submit(BeginHandoff("P", "h1", 50, children=("C1",)))

    def test_child_first_frontier_below_H_rejected(self):
        k = kernel()
        self._ready_parent(k)
        k.submit(BeginHandoff("P", "h1", 100, children=("C1",)))
        with self.assertRaises(ProtocolError):
            k.submit(ChildTakeover("h1", "C1", 1, 90))

    def test_handoff_retry_idempotent(self):
        k = kernel()
        self._ready_parent(k)
        k.submit(BeginHandoff("P", "h1", 100, children=("C1",)))
        r = k.submit(BeginHandoff("P", "h1", 100, children=("C1",)))
        self.assertEqual(r.disposition, "ignored")  # no duplicate registration
        with self.assertRaises(ProtocolError):
            k.submit(BeginHandoff("P", "h1", 100, children=("C9",)))
        k.submit(ChildTakeover("h1", "C1", 1, 100))
        r = k.submit(ChildTakeover("h1", "C1", 1, 100))
        self.assertEqual(r.disposition, "ignored")

    def test_cancel_pending_handoff(self):
        k = kernel()
        self._ready_parent(k)
        k.submit(BeginHandoff("P", "h1", 100, children=("C1",)))
        k.submit(CancelHandoff("h1"))
        # Parent still participates; child name is free again.
        k.submit(Progress(ProgressMessage("P", 1, 120, 2)))
        self.assertEqual(k.boundary, 120)

    def test_completed_handoff_cannot_be_cancelled(self):
        k = kernel()
        self._ready_parent(k)
        k.submit(BeginHandoff("P", "h1", 100, children=("C1",)))
        k.submit(ChildTakeover("h1", "C1", 1, 100))
        with self.assertRaises(ProtocolError):
            k.submit(CancelHandoff("h1"))

    def test_handoff_waits_for_parent_cut(self):
        k = kernel()
        k.submit(RegisterSource("P"))
        k.submit(Progress(ProgressMessage("P", 1, 100, 2)))
        k.submit(Data(DataMessage("P", 1, 1, 40)))
        k.submit(Data(DataMessage("P", 1, 2, 60)))
        k.submit(Ack("P", 1, 1))  # seq 2 not yet acked: water < 100
        k.submit(BeginHandoff("P", "h1", 100, children=("C1",)))
        k.submit(ChildTakeover("h1", "C1", 1, 100))
        self.assertEqual(k.state["handoffs"]["h1"]["status"], "pending")
        k.submit(Ack("P", 1, 2))  # now parent water reaches 100
        self.assertEqual(k.state["handoffs"]["h1"]["status"], "done")


if __name__ == "__main__":
    unittest.main()


class TestRecoveredSourceLateData(unittest.TestCase):
    def test_recovered_idle_source_data_below_closed_boundary_is_late(self):
        k = kernel()
        k.submit(RegisterSource("A"))
        k.submit(RegisterSource("B"))
        k.submit(Progress(ProgressMessage("A", 1, 50, 0)))
        k.submit(Progress(ProgressMessage("B", 1, 100, 0)))
        k.submit(IdleTimeout("A"))  # A idle-excluded; global = min(B) = 100
        assert k.boundary == 100
        # A comes back with data below the closed global boundary: late.
        r = k.submit(Data(DataMessage("A", 1, 1, 70)))
        assert r.disposition == "late"
        assert k.boundary == 100
        # Data at/above the closed boundary is still accepted normally.
        r = k.submit(Data(DataMessage("A", 1, 2, 120)))
        assert r.disposition == "accepted"
