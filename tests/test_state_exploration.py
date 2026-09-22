import itertools
import tempfile
import unittest

from watermark_kernel import DataMessage, ProgressMessage, WatermarkKernel

from tests.independent_oracle import IndependentWatermarkOracle


def watermark_orders():
    actions = ["d1", "d2", "d3", "a1", "a2", "a3", "pA", "pB"]
    for order in itertools.permutations(actions):
        indexes = {action: position for position, action in enumerate(order)}
        if not indexes["d1"] < indexes["d2"] < indexes["d3"]:
            continue
        if not indexes["a1"] < indexes["a2"] < indexes["a3"]:
            continue
        if any(indexes[f"d{seq}"] > indexes[f"a{seq}"] for seq in range(1, 4)):
            continue
        yield order


def handover_orders():
    groups = [("c1",), ("c2",)]
    for permutation in itertools.permutations(groups):
        order = tuple(item[0] for item in permutation)
        yield order


def run_watermark_order(directory, order):
    kernel = WatermarkKernel.open(directory, checkpoint_every=100)
    for source, epoch in [("A", "a"), ("B", "b")]:
        kernel.proposal(source, epoch)
        kernel.accept_source(source, epoch, 0)
    for action in order:
        if action.startswith("d"):
            seq = int(action[1:])
            kernel.data(DataMessage("A", "a", seq, seq))
        elif action.startswith("a"):
            seq = int(action[1:])
            kernel.ack("A", "a", seq)
        elif action == "pA":
            kernel.progress(ProgressMessage("A", "a", 100, 3))
        elif action == "pB":
            kernel.progress(ProgressMessage("B", "b", 80, 0))
    return [decision.boundary for decision in kernel.stable_decisions]


class StateExplorationTests(unittest.TestCase):
    def test_watermark_delivery_ack_orders(self):
        count = 0
        for order in watermark_orders():
            count += 1
            oracle = IndependentWatermarkOracle()
            for action in order:
                oracle.apply(action)
            with tempfile.TemporaryDirectory() as directory:
                decisions = run_watermark_order(directory, order)
            self.assertEqual(decisions, oracle.boundaries, order)
            self.assertTrue(all(left <= right for left, right in zip(decisions, decisions[1:])))
        self.assertEqual(count, 280)

    def test_handover_participant_orders(self):
        count = 0
        for order in handover_orders():
            count += 1
            with tempfile.TemporaryDirectory() as directory:
                kernel = WatermarkKernel.open(directory, checkpoint_every=100)
                kernel.proposal("P", "p")
                kernel.accept_source("P", "p", 0)
                kernel.progress(ProgressMessage("P", "p", 10, 1))
                for seq in (1, 2):
                    kernel.data(DataMessage("P", "p", seq, seq))
                    kernel.ack("P", "p", seq)
                kernel.begin_handover(
                    "split", "P", "p", 10, 1, [("C1", "c1"), ("C2", "c2")]
                )
                for action in order:
                    if action.startswith("c"):
                        child = action
                        kernel.confirm_child("split", child.upper(), child)
                self.assertEqual(set(kernel.sources), {"C1", "C2"})
                self.assertEqual(kernel.handovers["split"].status, "committed")
                self.assertEqual(kernel.boundary, 10)
        self.assertEqual(count, 2)


if __name__ == "__main__":
    unittest.main()
