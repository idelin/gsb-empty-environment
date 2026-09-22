import tempfile
import unittest

from watermark_kernel import CoordinatorNode, DownstreamNode, Message, Simulation
from watermark_kernel.errors import SimulatedProcessCrash


def one_shot(phase):
    def hook(current_phase, event):
        if not hook.fired and current_phase == phase and event.get("boundary") == 8:
            hook.fired = True
            raise SimulatedProcessCrash(phase)

    hook.fired = False
    return hook


class SimulatorTests(unittest.TestCase):
    def test_local_discrete_event_recovery_and_downstream_dedup(self):
        for phase in [
            "decision_pre_append",
            "decision_post_append_pre_publish",
            "decision_post_publish_pre_ack",
        ]:
            with self.subTest(phase=phase), tempfile.TemporaryDirectory() as directory:
                hook = one_shot(phase)
                downstream = DownstreamNode()
                coordinator = CoordinatorNode(
                    directory, downstream, fault_hook=hook, checkpoint_every=1
                )
                simulation = Simulation()
                simulation.start_coordinator(coordinator)
                for source, epoch in [("A", "a"), ("B", "b")]:
                    simulation.send(
                        coordinator,
                        "proposal",
                        {"source_id": source, "epoch": epoch},
                    )
                    simulation.send(
                        coordinator,
                        "accept_source",
                        {"source_id": source, "epoch": epoch, "accepted_floor": 0},
                    )
                simulation.send(
                    coordinator,
                    "progress",
                    {"source": "A", "epoch": "a", "frontier": 10, "cut_seq": 0},
                    delay=2,
                )
                simulation.send(
                    coordinator,
                    "progress",
                    {"source": "B", "epoch": "b", "frontier": 8, "cut_seq": 0},
                    delay=3,
                )
                simulation.run()
                self.assertTrue(hook.fired)
                self.assertEqual(coordinator.restart_count, 1)
                self.assertEqual([item.boundary for item in downstream.received], [8])
                self.assertEqual(len(downstream.seen), 1)
                simulation.send(
                    coordinator,
                    "ack_decision",
                    {"decision_id": "decision-000000000001"},
                )
                simulation.run()
                self.assertEqual(coordinator.kernel.unconfirmed_decisions, set())


if __name__ == "__main__":
    unittest.main()
