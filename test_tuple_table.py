"""unittest suite for the tuple-table CSP solver (Python 3.11, stdlib)."""

import itertools
import random
import unittest

from checker import (brute_force_solutions, is_satisfiable_brute_force,
                     verify_solution)
from csp import CSP, Solver, solve
from tuple_table import TupleTable


class TupleTableStructureTests(unittest.TestCase):
    def test_duplicate_tuples_not_counted_twice(self):
        table = TupleTable(2, [(0, 0), (0, 1), (0, 0), (1, 1), (0, 1)])
        self.assertEqual(len(table), 3)
        # support counts must reflect unique tuples only
        self.assertEqual(table._support_count[0][0], 2)
        self.assertEqual(table._support_count[1][1], 2)
        self.assertEqual(table.valid_count(), 3)

    def test_shared_prefix_compression(self):
        table = TupleTable(3, [(0, 0, 1), (0, 0, 2), (0, 1, 1), (1, 0, 1)])
        # flat storage: 4*3 = 12 cells; trie shares the (0,0) prefix etc.
        self.assertLess(table.node_count(), 12)
        self.assertEqual(table.node_count(), 9)
        self.assertIn((0, 0, 1), table)
        self.assertIn((0, 1, 1), table)
        self.assertNotIn((0, 0, 0), table)
        self.assertNotIn((1, 1, 1), table)

    def test_arity_mismatch_rejected(self):
        with self.assertRaises(ValueError):
            TupleTable(2, [(1, 2, 3)])

    def test_bad_polarity_rejected(self):
        with self.assertRaises(ValueError):
            TupleTable(1, [(1,)], polarity="sideways")


class SupportMaintenanceTests(unittest.TestCase):
    def test_remove_value_invalidates_related_tuples(self):
        table = TupleTable(2, [(0, 0), (0, 1), (1, 1), (2, 2)])
        trail = []
        table.remove_value(0, 0, trail)
        self.assertEqual(table.valid_count(), 2)
        self.assertFalse(table.has_support(0, 0))
        self.assertTrue(table.has_support(1, 1))   # still supported by (1,1)
        table.remove_value(0, 1, trail)
        self.assertFalse(table.has_support(1, 1))  # (1,1) now invalid too
        self.assertEqual(table.supported_values(1), {2})

    def test_restore_undoes_invalidation_exactly(self):
        table = TupleTable(2, [(0, 0), (0, 1), (1, 1), (2, 2)])
        trail = []
        table.remove_value(0, 0, trail)
        table.remove_value(1, 2, trail)
        self.assertEqual(table.valid_count(), 1)
        while trail:
            _, tbl, tid = trail.pop()
            tbl.restore_tuple(tid)
        self.assertEqual(table.valid_count(), 4)
        for pos in (0, 1):
            self.assertEqual(table.supported_values(pos), {0, 1, 2})

    def test_remove_absent_value_is_noop(self):
        table = TupleTable(1, [(0,)])
        trail = []
        table.remove_value(0, 99, trail)
        self.assertEqual(table.valid_count(), 1)
        self.assertEqual(trail, [])


class SolverSemanticsTests(unittest.TestCase):
    def test_empty_positive_table_is_infeasible(self):
        csp = CSP()
        csp.add_variable("x", {1, 2, 3})
        csp.add_constraint(["x"], [])
        self.assertIsNone(solve(csp))

    def test_empty_negative_table_is_no_restriction(self):
        csp = CSP()
        csp.add_variable("x", {1, 2})
        csp.add_variable("y", {"a", "b", "c"})
        csp.add_constraint(["x", "y"], [], polarity=TupleTable.NEGATIVE)
        solutions = list(brute_force_solutions(csp))
        self.assertEqual(len(solutions), 6)  # 2*3, nothing forbidden
        self.assertIsNotNone(solve(csp))

    def test_positive_table_solution_verified(self):
        csp = CSP()
        for v in "xyz":
            csp.add_variable(v, {0, 1, 2})
        csp.add_constraint(["x", "y", "z"],
                           [(0, 0, 1), (0, 0, 2), (0, 1, 1), (2, 2, 0)])
        solution = solve(csp)
        self.assertIsNotNone(solution)
        self.assertTrue(verify_solution(csp, solution))
        self.assertEqual(sum(1 for _ in brute_force_solutions(csp)), 4)

    def test_negative_table_triangle_coloring(self):
        csp = CSP()
        colors = {"r", "g", "b"}
        for v in "abc":
            csp.add_variable(v, colors)
        forbidden = [(c, c) for c in colors]
        for u, v in [("a", "b"), ("b", "c"), ("a", "c")]:
            csp.add_constraint([u, v], forbidden,
                               polarity=TupleTable.NEGATIVE)
        solution = solve(csp)
        self.assertIsNotNone(solution)
        self.assertTrue(verify_solution(csp, solution))
        self.assertEqual(len({solution["a"], solution["b"], solution["c"]}),
                         3)
        self.assertEqual(sum(1 for _ in brute_force_solutions(csp)), 6)

    def test_negative_table_can_make_model_infeasible(self):
        csp = CSP()
        csp.add_variable("x", {1, 2})
        csp.add_constraint(["x"], [(1,), (2,)], polarity=TupleTable.NEGATIVE)
        self.assertIsNone(solve(csp))
        self.assertFalse(is_satisfiable_brute_force(csp))

    def test_mixed_positive_and_negative(self):
        csp = CSP()
        for v in "xy":
            csp.add_variable(v, {0, 1, 2})
        csp.add_constraint(["x", "y"],
                           [(0, 0), (0, 1), (1, 1), (2, 2), (2, 0)])
        csp.add_constraint(["x", "y"], [(0, 0), (2, 2)],
                           polarity=TupleTable.NEGATIVE)
        solution = solve(csp)
        self.assertIsNotNone(solution)
        self.assertTrue(verify_solution(csp, solution))
        allowed = {(0, 1), (1, 1), (2, 0)}
        self.assertEqual(
            {(s["x"], s["y"]) for s in brute_force_solutions(csp)}, allowed)

    def test_domain_removal_cascades_through_supports(self):
        csp = CSP()
        csp.add_variable("x", {0, 1})
        csp.add_variable("y", {0, 1})
        csp.add_constraint(["x", "y"], [(0, 0), (1, 1)])
        solver = Solver(csp)
        solver.prune("x", 0)
        self.assertTrue(solver.propagate())
        self.assertEqual(solver.domains["y"], {1})  # y=0 lost its support

    def test_domain_removal_can_detect_infeasibility(self):
        csp = CSP()
        csp.add_variable("x", {0, 1})
        csp.add_variable("y", {0, 1})
        csp.add_constraint(["x", "y"], [(0, 0), (1, 1)])
        solver = Solver(csp)
        solver.prune("x", 0)
        solver.prune("y", 1)
        self.assertFalse(solver.propagate())  # wipeout: no valid tuple left

    def test_checker_rejects_bad_solutions(self):
        csp = CSP()
        csp.add_variable("x", {0, 1})
        csp.add_constraint(["x"], [(0,)])
        self.assertFalse(verify_solution(csp, {"x": 1}))
        self.assertFalse(verify_solution(csp, {"x": 7}))   # outside domain
        self.assertFalse(verify_solution(csp, {"y": 0}))   # wrong variable
        self.assertFalse(verify_solution(csp, None))


class RandomizedCrossCheckTests(unittest.TestCase):
    """Solver verdicts must match exhaustive enumeration on random CSPs."""

    def test_solver_matches_brute_force(self):
        rng = random.Random(20261001)
        for case in range(200):
            n_vars = rng.randint(2, 4)
            csp = CSP()
            names = [f"v{i}" for i in range(n_vars)]
            for name in names:
                csp.add_variable(name, set(range(rng.randint(1, 3))))
            for _ in range(rng.randint(1, 3)):
                scope = rng.sample(names, rng.randint(1, min(3, n_vars)))
                cells = [csp.domains[v] for v in scope]
                tuples = [t for t in itertools.product(*cells)
                          if rng.random() < 0.5]
                if rng.random() < 0.3 and tuples:  # inject duplicates
                    tuples += rng.sample(tuples, 1)
                polarity = (TupleTable.POSITIVE if rng.random() < 0.5
                            else TupleTable.NEGATIVE)
                csp.add_constraint(scope, tuples, polarity=polarity)
            solution = solve(csp)
            if is_satisfiable_brute_force(csp):
                self.assertIsNotNone(solution, f"case {case}: missed solution")
                self.assertTrue(verify_solution(csp, solution))
            else:
                self.assertIsNone(solution, f"case {case}: phantom solution")


if __name__ == "__main__":
    unittest.main()
