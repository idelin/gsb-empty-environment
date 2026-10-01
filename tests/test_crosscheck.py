import unittest

from modelcheck import (CounterModel, ExplicitModel, MutexModel,
                        PermutationNormalizer, run_bfs)
from modelcheck.reference import reference_bfs


class TestCrossCheck(unittest.TestCase):
    """Engine (compressed store + disk frontier) vs. independent naive BFS."""

    def _check(self, model, normalizer=None):
        res = run_bfs(model, normalizer=normalizer, collect=True)
        ref = reference_bfs(model, normalizer)
        self.assertEqual(res.status, "safe")
        self.assertEqual(set(res.reachable), set(ref["distances"]))
        for key, (_, dist) in res.reachable.items():
            self.assertEqual(dist, ref["distances"][key],
                             f"distance mismatch for {res.reachable[key][0]!r}")
        return res, ref

    def test_mutex_fixed(self):
        self._check(MutexModel(3, fixed=True))

    def test_explicit_graph(self):
        model = ExplicitModel(
            initials=[0],
            transitions={0: [("a", 1), ("b", 2)], 1: [("c", 3)],
                         2: [("d", 3)], 3: [("e", 4)], 4: []},
            invariants=[("lt5", lambda s: s < 5)],
        )
        self._check(model)

    def test_symmetric_counters_with_normalization(self):
        model = CounterModel(k=3, max_count=2)
        norm = PermutationNormalizer()
        res, _ = self._check(model, norm)
        # normalization really collapses symmetric states
        raw = reference_bfs(model)  # identity normalization
        self.assertEqual(len(raw["distances"]), 27)
        self.assertEqual(len(res.reachable), 10)

    def test_violation_distance_matches_reference(self):
        model = MutexModel(2, fixed=False)
        res = run_bfs(model)
        ref = reference_bfs(model)
        self.assertEqual(res.status, "violated")
        self.assertEqual(len(res.trace), ref["first_violation"][0])
        self.assertEqual(res.trace.violated_invariant, ref["first_violation"][1])


if __name__ == "__main__":
    unittest.main()
