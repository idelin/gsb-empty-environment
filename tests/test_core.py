import unittest

from muskit.cache import CachedSolver
from muskit.checker import is_satisfying
from muskit.core import (
    NOT_CONFLICTING,
    SUBSET_MINIMAL,
    UNDECIDED,
    extract_subset_minimal_core,
)
from muskit.solver import SAT, UNKNOWN, UNSAT, SolveResult
from muskit.demo import build_problem


def scripted_solve(table):
    """Fake solve fn: maps frozenset(ids) -> (status, assignment)."""
    def solve(ids, budget=None):
        status, assignment = table[frozenset(ids)]
        return SolveResult(status, assignment, nodes=1, budget=budget)
    return solve


class DeletionCoreTest(unittest.TestCase):
    def setUp(self):
        self.problem = build_problem()
        self.solver = CachedSolver(self.problem)

    def test_subset_minimal_core_with_log_and_witnesses(self):
        result = extract_subset_minimal_core(self.problem, self.solver.solve)
        self.assertEqual(result.status, SUBSET_MINIMAL)
        self.assertEqual(result.core, ["c", "d", "e"])  # a, b deleted first
        self.assertEqual(len(result.log), 5)
        decisions = {r.cid: r.decision for r in result.log}
        self.assertEqual(decisions["a"], "removed")
        self.assertEqual(decisions["b"], "removed")
        for cid in ("c", "d", "e"):
            self.assertEqual(decisions[cid], "kept-sat")
            witness = result.deletion_witness(cid)
            remainder = [x for x in result.core if x != cid]
            self.assertTrue(is_satisfying(self.problem, remainder, witness))

    def test_subset_minimal_is_not_minimum_cardinality(self):
        result = extract_subset_minimal_core(self.problem, self.solver.solve)
        self.assertEqual(len(result.core), 3)
        # the minimum-cardinality core {a, b} has size 2
        self.assertEqual(self.solver.solve(["a", "b"]).status, UNSAT)

    def test_not_conflicting(self):
        result = extract_subset_minimal_core(self.problem, self.solver.solve, ["a", "c"])
        self.assertEqual(result.status, NOT_CONFLICTING)

    def test_unknown_on_initial_check_propagates(self):
        result = extract_subset_minimal_core(
            self.problem, self.solver.solve, budget_per_check=1)
        self.assertEqual(result.status, UNDECIDED)
        self.assertEqual(result.log, [])

    def test_unknown_mid_loop_keeps_item_and_blocks_minimality_claim(self):
        # Full set {a,c,d} declared UNSAT; removing "a" is undecided; the other
        # trials are SAT with witnesses valid against the real constraints.
        full = frozenset({"a", "c", "d"})
        table = {
            full: (UNSAT, None),
            frozenset({"c", "d"}): (UNKNOWN, None),          # removing "a"
            frozenset({"a", "d"}): (SAT, {"x": 0, "y": 0}),  # removing "c"
            frozenset({"a", "c"}): (SAT, {"x": 0, "y": 1}),  # removing "d"
        }
        result = extract_subset_minimal_core(self.problem, scripted_solve(table),
                                             ["a", "c", "d"])
        self.assertEqual(result.status, UNDECIDED)
        self.assertIn("a", result.core)  # unknown is NOT treated as UNSAT
        record = next(r for r in result.log if r.cid == "a")
        self.assertEqual(record.decision, "kept-undecided")

    def test_unknown_is_not_treated_as_sat_either(self):
        # If UNKNOWN were treated as SAT the item would be kept with a fake
        # witness; ensure no witness is fabricated.
        full = frozenset({"a", "b"})
        table = {
            full: (UNSAT, None),
            frozenset({"a"}): (UNKNOWN, None),
            frozenset({"b"}): (UNKNOWN, None),
        }
        result = extract_subset_minimal_core(self.problem, scripted_solve(table),
                                             ["a", "b"])
        self.assertEqual(result.status, UNDECIDED)
        self.assertEqual(result.witnesses, {})


if __name__ == "__main__":
    unittest.main()
