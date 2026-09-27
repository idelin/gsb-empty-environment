"""Unit tests for the DPLL solver (stdlib unittest only)."""

import random
import unittest

from dpll import DPLLSolver, brute_force_sat, verify_model


def pigeonhole(pigeons, holes):
    """Return (clauses, num_vars) encoding the pigeonhole principle."""
    clauses = []

    def var(p, h):
        return p * holes + h + 1

    for p in range(pigeons):
        clauses.append([var(p, h) for h in range(holes)])
    for h in range(holes):
        for p1 in range(pigeons):
            for p2 in range(p1 + 1, pigeons):
                clauses.append([-var(p1, h), -var(p2, h)])
    return clauses, pigeons * holes


class DPLLBasicsTest(unittest.TestCase):
    def test_empty_formula_is_sat(self):
        solver = DPLLSolver(num_vars=3)
        model = solver.solve()
        self.assertIsNotNone(model)
        self.assertTrue(verify_model([], model))
        self.assertEqual(set(model), {1, 2, 3})

    def test_empty_clause_is_immediately_unsat(self):
        solver = DPLLSolver(num_vars=2)
        solver.add_clause([1, 2])
        solver.add_clause([])
        solver.add_clause([-1])
        self.assertIsNone(solver.solve())

    def test_tautological_clause_is_ignored(self):
        solver = DPLLSolver(num_vars=2)
        solver.add_clause([1, -1])
        solver.add_clause([2, -2, 1])
        model = solver.solve()
        self.assertIsNotNone(model)
        self.assertTrue(verify_model(solver.original_clauses, model))

    def test_contradictory_units_are_unsat(self):
        solver = DPLLSolver(num_vars=1)
        solver.add_clause([1])
        solver.add_clause([-1])
        self.assertIsNone(solver.solve())

    def test_unit_propagation_chain(self):
        # x1; ~x1 v x2; ~x2 v x3  =>  x1=x2=x3=true
        solver = DPLLSolver(num_vars=3)
        solver.add_clause([1])
        solver.add_clause([-1, 2])
        solver.add_clause([-2, 3])
        model = solver.solve()
        self.assertEqual(model, {1: True, 2: True, 3: True})
        self.assertTrue(verify_model(solver.original_clauses, model))

    def test_reported_model_always_verified(self):
        solver = DPLLSolver(num_vars=4)
        for clause in [[1, 2], [-1, 3], [-2, -3], [1, -4], [4, 2]]:
            solver.add_clause(clause)
        model = solver.solve()
        self.assertIsNotNone(model)
        self.assertTrue(verify_model(solver.original_clauses, model))


class DPLLBacktrackingTest(unittest.TestCase):
    def test_unsat_requires_exploring_both_branches(self):
        # x1 -> (x3 & ~x3);  ~x1 -> (x2 & ~x2), so the formula is UNSAT.
        solver = DPLLSolver(num_vars=3)
        for clause in [[1, 2], [1, -2], [-1, 3], [-1, -3]]:
            solver.add_clause(clause)
        self.assertIsNone(solver.solve())

    def test_backtrack_does_not_miss_propagation(self):
        # On the first branch x4=true a later decision conflicts and forces
        # backtracking; the returned model must still satisfy all clauses,
        # meaning watch/trail restoration kept propagation complete.
        solver = DPLLSolver(num_vars=4)
        clauses = [
            [4, 1], [4, 2], [-1, -2, 3],
            [-4, 1], [-4, -1], [2, 3],
        ]
        for clause in clauses:
            solver.add_clause(clause)
        model = solver.solve()
        self.assertIsNotNone(model)
        self.assertTrue(verify_model(clauses, model))

    def test_pigeonhole_4_3_unsat(self):
        clauses, n = pigeonhole(4, 3)
        solver = DPLLSolver(num_vars=n)
        for clause in clauses:
            solver.add_clause(clause)
        self.assertIsNone(solver.solve())


class AssumptionTest(unittest.TestCase):
    def setUp(self):
        self.clauses = [[1, 2]]   # x1 v x2
        self.solver = DPLLSolver(num_vars=2)
        self.solver.add_clause(self.clauses[0])

    def test_assumptions_force_unit_propagation(self):
        model = self.solver.solve(assumptions=[-1])
        self.assertIsNotNone(model)
        self.assertFalse(model[1])
        self.assertTrue(model[2])

    def test_assumptions_are_retracted_after_query(self):
        self.assertIsNone(self.solver.solve(assumptions=[-1, -2]))
        model = self.solver.solve(assumptions=[-1])   # x2 must still be free to rise
        self.assertTrue(model[2])
        model = self.solver.solve()                    # no assumptions: still SAT
        self.assertTrue(model[1] or model[2])

    def test_assumption_contradicts_persistent_unit(self):
        solver = DPLLSolver(num_vars=1)
        solver.add_clause([1])
        self.assertIsNone(solver.solve(assumptions=[-1]))
        self.assertIsNotNone(solver.solve())           # unit itself survives retraction

    def test_repeated_queries_with_different_hypotheses(self):
        solver = DPLLSolver(num_vars=3)
        for clause in [[-1, 2], [-2, 3]]:
            solver.add_clause(clause)
        under_x1 = solver.solve(assumptions=[1])
        self.assertTrue(under_x1[2] and under_x1[3])
        under_not_x3 = solver.solve(assumptions=[-3])
        self.assertFalse(under_not_x3[2])
        self.assertFalse(under_not_x3[1])


class IncrementalAndRandomTest(unittest.TestCase):
    def test_clauses_added_between_queries(self):
        solver = DPLLSolver(num_vars=2)
        solver.add_clause([1, 2])
        model = solver.solve()
        self.assertIsNotNone(model)
        solver.add_clause([-1])
        solver.add_clause([-2])
        self.assertIsNone(solver.solve())

    def test_random_cnf_matches_brute_force(self):
        rng = random.Random(20260927)
        for trial in range(150):
            n = rng.randint(1, 6)
            m = rng.randint(0, 3 * n + 2)
            clauses = []
            for _ in range(m):
                size = rng.randint(0, 3)
                clauses.append([
                    rng.choice([-1, 1]) * rng.randint(1, n) for _ in range(size)
                ])

            solver = DPLLSolver(num_vars=n)
            for clause in clauses:
                solver.add_clause(clause)

            # Solve twice so assumption push/retract is exercised as well.
            assumption = [rng.choice([-1, 1])] if n >= 1 and rng.random() < 0.5 else []
            model = solver.solve(assumptions=assumption)
            model_again = solver.solve(assumptions=assumption)

            ref = brute_force_sat(
                clauses + [[lit] for lit in assumption], n
            )
            self.assertIsNotNone(model_again) if model is not None else self.assertIsNone(model_again)
            if model is not None:
                self.assertTrue(verify_model(clauses, model))
                for lit in assumption:
                    self.assertEqual(model[abs(lit)], lit > 0)
                self.assertIsNotNone(ref)
            else:
                self.assertIsNone(ref, msg=f"trial {trial}: {clauses} under {assumption}")


if __name__ == "__main__":
    unittest.main(verbosity=2)
