import tempfile
import unittest

from watermark_kernel import DataMessage, Decision, ProgressMessage, WatermarkKernel
from watermark_kernel.errors import SimulatedProcessCrash


def one_shot(phase, decision_number=1, handover_id="split", boundary=None):
    def hook(current_phase, event):
        if current_phase == phase:
            if (
                event.get("decision_id") == f"decision-{decision_number:012d}"
                and (boundary is None or event.get("boundary") == boundary)
            ):
                hook.fired = True
                raise SimulatedProcessCrash(phase)
            if event.get("handover_id") == handover_id:
                hook.fired = True
                raise SimulatedProcessCrash(phase)

    hook.fired = False
    return hook


def setup_boundary(directory, hook=None):
    decisions = []
    kernel = WatermarkKernel.open(
        directory, checkpoint_every=1, fault_hook=hook, publisher=decisions.append
    )
    for source, epoch in [("A", "a"), ("B", "b")]:
        kernel.proposal(source, epoch)
        kernel.accept_source(source, epoch, 0)
    kernel.progress(ProgressMessage("A", "a", 10, 0))
    kernel.progress(ProgressMessage("B", "b", 8, 0))
    return kernel, decisions


def run_boundary_fixture(directory, hook=None):
    _, decisions = setup_boundary(directory, hook)
    return [(decision.decision_id, decision.boundary) for decision in decisions]


class FaultTests(unittest.TestCase):
    def test_decision_fault_points_preserve_stable_id(self):
        phases = [
            "decision_pre_append",
            "decision_post_append_pre_publish",
            "decision_post_publish_pre_ack",
        ]
        for phase in phases:
            with self.subTest(phase=phase), tempfile.TemporaryDirectory() as directory:
                with tempfile.TemporaryDirectory() as baseline_directory:
                    expected = run_boundary_fixture(baseline_directory)
                hook = one_shot(phase, boundary=8)
                with self.assertRaises(SimulatedProcessCrash):
                    setup_boundary(directory, hook)
                self.assertTrue(hook.fired)
                decisions = []
                recovered = WatermarkKernel.open(directory, checkpoint_every=1, publisher=decisions.append)
                actual = [(decision.decision_id, decision.boundary) for decision in decisions]
                self.assertEqual(actual[-1:], expected)
                self.assertEqual({item.decision_id for item in decisions}, {"decision-000000000001"})
                recovered.ack_decision("decision-000000000001")
                final = WatermarkKernel.open(directory, checkpoint_every=1, publisher=decisions.append)
                self.assertEqual(final.boundary, 8)
                self.assertEqual(final.unconfirmed_decisions, set())

    def test_handover_fault_points_complete_atomically(self):
        phases = ["handover_pre_commit_record", "handover_post_commit_record", "handover_post_commit_state"]
        for phase in phases:
            with self.subTest(phase=phase), tempfile.TemporaryDirectory() as directory:
                hook = one_shot(phase)
                kernel = WatermarkKernel.open(directory, checkpoint_every=1, fault_hook=hook)
                kernel.proposal("P", "p")
                kernel.accept_source("P", "p", 0)
                kernel.progress(ProgressMessage("P", "p", 10, 1))
                kernel.data(DataMessage("P", "p", 1, 1))
                kernel.ack("P", "p", 1)
                kernel.begin_handover("split", "P", "p", 10, 1, [("C1", "c1"), ("C2", "c2")])
                kernel.confirm_child("split", "C1", "c1")
                with self.assertRaises(SimulatedProcessCrash):
                    kernel.confirm_child("split", "C2", "c2")

                recovered = WatermarkKernel.open(directory, checkpoint_every=1)
                self.assertEqual(set(recovered.sources), {"C1", "C2"})
                self.assertEqual(recovered.handovers["split"].status, "committed")


if __name__ == "__main__":
    unittest.main()
