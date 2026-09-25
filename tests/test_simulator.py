import unittest

from checkpoint.engine import Engine
from checkpoint.planner import gradient_workspace, plan_chain_optimal
from checkpoint.simulator import SimViolation, simulate
from checkpoint.standard_nodes import build_chain


class TestSimulator(unittest.TestCase):
    def test_engine_events_are_consistent(self):
        nodes, input_names, inputs = build_chain([4, 6, 6, 3], noise=0.1,
                                                 seed=5)
        engine = Engine(nodes, input_names)
        full = engine.run(inputs, checkpoints=[n.name for n in nodes],
                          seed=1)
        sizes = [inputs["x"].nbytes]
        sizes += [8 * len(full.audit[nm][1]) for nm in engine.topo]
        costs = [0.0] + [engine.nodes[nm].cost for nm in engine.topo]
        workspace = gradient_workspace(
            [t.nbytes for t in inputs.values()], max(sizes))
        budget = workspace + 2 * max(sizes) + sum(sizes) // 2
        plan = plan_chain_optimal(sizes, costs, budget, workspace)
        names = [engine.topo[k - 1] for k in plan[0] if k > 0]
        res = engine.run(inputs, checkpoints=names, seed=1)
        report = simulate(res.events)
        # no read-after-free occurred and the peak matches the engine's
        self.assertEqual(report["peak"], res.stats.peak_bytes)
        self.assertEqual(report["live_at_end"], [])
        self.assertGreater(report["reads"], 0)

    def test_read_after_free_is_caught(self):
        events = [("alloc", "a", 8), ("free", "a", 8), ("read", "a", 0)]
        with self.assertRaises(SimViolation):
            simulate(events)

    def test_double_free_is_caught(self):
        events = [("alloc", "a", 8), ("free", "a", 8), ("free", "a", 8)]
        with self.assertRaises(SimViolation):
            simulate(events)

    def test_double_alloc_is_caught(self):
        events = [("alloc", "a", 8), ("alloc", "a", 8)]
        with self.assertRaises(SimViolation):
            simulate(events)


if __name__ == "__main__":
    unittest.main()
