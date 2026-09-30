import unittest

from muskit.cache import CachedSolver
from muskit.demo import build_problem
from muskit.hitting import (
    NOT_CONFLICTING,
    OPTIMAL,
    UNDECIDED,
    _max_disjoint_packing,
    find_min_cardinality_core,
)
from muskit.solver import SAT, UNKNOWN, UNSAT, SolveResult


class HittingSetTest(unittest.TestCase):
    def setUp(self):
        self.problem = build_problem()
        self.solver = CachedSolver(self.problem)

    def test_finds_proven_minimum_cardinality_core(self):
        result = find_min_cardinality_core(self.problem, self.solver.solve)
        self.assertEqual(result.status, OPTIMAL)
        self.assertTrue(result.proven)
        self.assertEqual(result.best_core, ["a", "b"])
        self.assertEqual(result.upper_bound, 2)
        self.assertEqual(result.lower_bound, 2)  # disjoint correction sets
        # lower-bound evidence: two pairwise-disjoint correction sets exist
        corr = [set(c) for c in result.correction_sets]
        self.assertEqual(_max_disjoint_packing(corr), 2)

    def test_correction_sets_are_hit_by_every_core(self):
        result = find_min_cardinality_core(self.problem, self.solver.solve)
        for core in result.cores:
            for corr in result.correction_sets:
                self.assertTrue(set(core) & set(corr))

    def test_not_conflicting(self):
        result = find_min_cardinality_core(self.problem, self.solver.solve, ["a", "c"])
        self.assertEqual(result.status, NOT_CONFLICTING)
        self.assertIsNone(result.best_core)

    def test_candidate_budget_exhaustion_is_undecided(self):
        result = find_min_cardinality_core(self.problem, self.solver.solve,
                                           candidate_budget=1)
        self.assertEqual(result.status, UNDECIDED)
        self.assertFalse(result.proven)

    def test_solver_unknown_never_counts_as_unsat(self):
        # Solver that can only decide the empty set; everything else UNKNOWN.
        def flaky(ids, budget=None):
            if not list(ids):
                return SolveResult(SAT, {}, 0, budget)
            return SolveResult(UNKNOWN, None, 0, budget)

        result = find_min_cardinality_core(self.problem, flaky, ["a", "b"])
        self.assertEqual(result.status, UNDECIDED)
        self.assertEqual(result.cores, [])  # no core may be recorded
        self.assertFalse(result.proven)

    def test_lower_bound_packing(self):
        self.assertEqual(_max_disjoint_packing([{"a", "b"}, {"c"}, {"a"}, {"d", "e"}]), 3)
        self.assertEqual(_max_disjoint_packing([]), 0)


if __name__ == "__main__":
    unittest.main()
