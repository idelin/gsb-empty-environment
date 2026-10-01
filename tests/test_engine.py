import json
import tempfile
import unittest
from pathlib import Path

from modelcheck import (Budget, CounterModel, ExplicitModel, MutexModel,
                        PermutationNormalizer, replay_trace, run_bfs)
from modelcheck.reference import reference_bfs


class TestEngine(unittest.TestCase):
    def test_safe_model(self):
        res = run_bfs(MutexModel(2, fixed=True))
        self.assertEqual(res.status, "safe")
        self.assertIsNone(res.trace)
        self.assertEqual(res.stats.stop_reason, "explored")

    def test_violation_shortest_trace_and_replay(self):
        model = MutexModel(2, fixed=False)
        res = run_bfs(model)
        self.assertEqual(res.status, "violated")
        ref = reference_bfs(model)
        # engine trace length equals the reference's first-violation depth
        self.assertEqual(len(res.trace), ref["first_violation"][0])
        self.assertEqual(res.trace.violated_invariant, "mutex")
        self.assertEqual(len(res.trace.states), len(res.trace.actions) + 1)
        self.assertTrue(replay_trace(model, None, res.trace))

    def test_initial_state_violation_length_zero(self):
        model = ExplicitModel(initials=["bad"], transitions={},
                              invariants=[("never-bad", lambda s: s != "bad")])
        res = run_bfs(model)
        self.assertEqual(res.status, "violated")
        self.assertEqual(len(res.trace), 0)
        self.assertEqual(res.trace.states, ["bad"])
        self.assertEqual(res.trace.actions, [])
        self.assertTrue(replay_trace(model, None, res.trace))

    def test_unreachable_bad_state_is_not_a_counterexample(self):
        model = ExplicitModel(
            initials=["a"],
            transitions={"a": [("go", "b")], "b": [],
                         "bad0": [("go", "bad1")], "bad1": []},
            invariants=[("ok", lambda s: not s.startswith("bad"))],
        )
        res = run_bfs(model)
        self.assertEqual(res.status, "safe")
        self.assertEqual(res.stats.states_discovered, 2)

    def test_pending_with_frontier_evidence(self):
        model = MutexModel(3, fixed=True)
        with tempfile.TemporaryDirectory() as tmp:
            res = run_bfs(model, budget=Budget(max_layers=2), run_dir=tmp, collect=True)
            self.assertEqual(res.status, "pending")
            self.assertIn("layer budget", res.stats.stop_reason)
            evidence = json.loads(Path(res.frontier_evidence).read_text())
            self.assertEqual(evidence["status"], "pending")
            self.assertEqual(evidence["unexplored_layer"], 2)
            # unexplored frontier evidence == reference BFS layer at that depth
            ref = reference_bfs(model)
            expected = {k.hex() for k, d in ref["distances"].items() if d == 2}
            got = {entry["key"] for entry in evidence["unexplored_frontier"]}
            self.assertEqual(got, expected)
            self.assertTrue(expected)  # evidence is non-empty
            # coverage statistics are reported
            self.assertEqual(evidence["coverage"]["layers_completed"], 2)
            self.assertGreater(evidence["coverage"]["states_discovered"], 0)

    def test_state_budget(self):
        res = run_bfs(MutexModel(3, fixed=True), budget=Budget(max_states=2))
        self.assertEqual(res.status, "pending")
        self.assertIn("state budget", res.stats.stop_reason)

    def test_time_budget_zero(self):
        res = run_bfs(MutexModel(2, fixed=True), budget=Budget(max_seconds=0.0))
        self.assertEqual(res.status, "pending")
        self.assertIn("time budget", res.stats.stop_reason)

    def test_pending_is_not_safe(self):
        # an unexplored model must never be reported safe
        res = run_bfs(MutexModel(2, fixed=False), budget=Budget(max_layers=1))
        self.assertEqual(res.status, "pending")
        self.assertIsNone(res.trace)

    def test_parallel_expansion_preserves_layers(self):
        model = MutexModel(3, fixed=True)
        sequential = run_bfs(model, collect=True)
        parallel = run_bfs(model, collect=True, workers=4)
        self.assertEqual(sequential.status, parallel.status)
        self.assertEqual(set(sequential.reachable), set(parallel.reachable))
        for key, (_, dist) in sequential.reachable.items():
            self.assertEqual(dist, parallel.reachable[key][1])

    def test_normalized_trace_replays_against_concrete_model(self):
        model = CounterModel(k=3, max_count=3, sum_limit=4)
        norm = PermutationNormalizer()
        res = run_bfs(model, normalizer=norm)
        self.assertEqual(res.status, "violated")
        self.assertTrue(replay_trace(model, norm, res.trace))
        # replayed trace ends in a concrete state that really violates
        final = res.trace.states[-1]
        self.assertGreater(sum(final), 4)


if __name__ == "__main__":
    unittest.main()
