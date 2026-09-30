import unittest

from muskit.cache import CachedSolver
from muskit.constraints import Constraint
from muskit.demo import build_problem
from muskit.solver import UNKNOWN, UNSAT


class CacheTest(unittest.TestCase):
    def setUp(self):
        self.problem = build_problem()
        self.solver = CachedSolver(self.problem)

    def test_repeated_assumption_set_hits_cache(self):
        self.solver.solve(["a", "b"])
        self.assertEqual(self.solver.misses, 1)
        again = self.solver.solve(["b", "a"])  # order-insensitive
        self.assertEqual(self.solver.hits, 1)
        self.assertEqual(again.status, UNSAT)

    def test_unknown_entries_are_budget_specific(self):
        tight = self.solver.solve(["a", "b", "c", "d", "e"], budget=1)
        self.assertEqual(tight.status, UNKNOWN)
        loose = self.solver.solve(["a", "b", "c", "d", "e"])  # no budget -> miss
        self.assertEqual(loose.status, UNSAT)
        self.assertEqual(self.solver.misses, 2)

    def test_pool_mutation_bumps_version_and_invalidates(self):
        self.solver.solve(["a", "b"])
        self.assertEqual(self.solver.misses, 1)
        revision = self.problem.revision
        self.problem.add_constraint(Constraint("f", ("x",), lambda x: x >= 0))
        self.assertGreater(self.problem.revision, revision)
        self.solver.solve(["a", "b"])  # same ids, but version changed
        self.assertEqual(self.solver.misses, 2)
        self.assertEqual(self.solver.hits, 0)

    def test_invalidate_clears(self):
        self.solver.solve(["a"])
        self.solver.invalidate()
        self.assertEqual(self.solver.size, 0)


if __name__ == "__main__":
    unittest.main()
