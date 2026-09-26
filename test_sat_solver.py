"""Unit tests for sat_solver (stdlib unittest only)."""

import random
import unittest

from sat_solver import (
    FiniteDomainEncoder,
    Solver,
    Status,
    check_model,
    first_violated,
)


def brute_force(clauses, n_vars, assumptions=()):
    """Exhaustive oracle: return a satisfying model or None."""
    for bits in range(1 << n_vars):
        model = {v: bool((bits >> (v - 1)) & 1) for v in range(1, n_vars + 1)}
        if any(model[abs(lit)] != (lit > 0) for lit in assumptions):
            continue
        if all(any(model[abs(lit)] == (lit > 0) for lit in c) for c in clauses):
            return model
    return None


class EdgeCaseTests(unittest.TestCase):
    def test_empty_formula_is_sat(self):
        res = Solver().solve()
        self.assertEqual(res.status, Status.SAT)
        self.assertEqual(res.model, {})
        self.assertTrue(res.verified)

    def test_empty_clause_is_immediate_unsat(self):
        solver = Solver()
        solver.add_clause([1, 2])
        solver.add_clause([])
        res = solver.solve()
        self.assertEqual(res.status, Status.UNSAT)
        self.assertIn("level 0", res.explanation)
        self.assertEqual(res.stats.decisions, 0)  # no search was needed

    def test_tautology_is_ignored(self):
        solver = Solver()
        self.assertIsNone(solver.add_clause([1, -1, 2]))
        self.assertEqual(solver.n_working_clauses, 0)
        res = solver.solve()
        self.assertEqual(res.status, Status.SAT)

    def test_duplicate_literals_removed(self):
        solver = Solver()
        solver.add_clause([1, 1, 1])
        self.assertEqual(solver.clauses[0], [1])
        res = solver.solve()
        self.assertTrue(res.model[1])

    def test_contradictory_units_unsat(self):
        solver = Solver()
        solver.add_clause([1])
        solver.add_clause([-1])
        res = solver.solve()
        self.assertEqual(res.status, Status.UNSAT)

    def test_literal_zero_rejected(self):
        solver = Solver()
        with self.assertRaises(ValueError):
            solver.add_clause([0, 1])
        with self.assertRaises(ValueError):
            solver.solve(assumptions=[0])


class PropagationAndSearchTests(unittest.TestCase):
    def test_unit_propagation_chain(self):
        solver = Solver()
        solver.add_clause([1])          # a
        solver.add_clause([-1, 2])      # a -> b
        solver.add_clause([-2, 3])      # b -> c
        res = solver.solve()
        self.assertEqual(res.status, Status.SAT)
        self.assertTrue(res.model[1] and res.model[2] and res.model[3])
        self.assertTrue(res.verified)

    def test_backtracking_finds_model(self):
        # Branching on x=True first fails: (-x|y) & (-x|-y) force y and -y.
        solver = Solver()
        solver.add_clause([-1, 2])
        solver.add_clause([-1, -2])
        solver.add_clause([1, 2])
        res = solver.solve()
        self.assertEqual(res.status, Status.SAT)
        self.assertFalse(res.model[1])
        self.assertTrue(res.model[2])
        self.assertGreaterEqual(res.stats.backtracks, 1)
        self.assertTrue(check_model(solver.original_clauses, res.model))

    def test_simple_unsat_formula(self):
        solver = Solver()
        for clause in ([1, 2], [-1, 2], [1, -2], [-1, -2]):
            solver.add_clause(clause)
        res = solver.solve()
        self.assertEqual(res.status, Status.UNSAT)
        self.assertIn("exhaustive", res.explanation)

    def test_pigeonhole_4_into_3_unsat(self):
        pigeons, holes = 4, 3
        solver = Solver()
        var = lambda p, h: p * holes + h + 1
        for p in range(pigeons):  # every pigeon in some hole
            solver.add_clause([var(p, h) for h in range(holes)])
        for h in range(holes):  # no hole holds two pigeons
            for p1 in range(pigeons):
                for p2 in range(p1 + 1, pigeons):
                    solver.add_clause([-var(p1, h), -var(p2, h)])
        res = solver.solve()
        self.assertEqual(res.status, Status.UNSAT)
        self.assertIn("Every branch", res.explanation)
        self.assertGreater(res.stats.conflicts, 0)

    def test_pigeonhole_3_into_3_sat(self):
        pigeons, holes = 3, 3
        solver = Solver()
        var = lambda p, h: p * holes + h + 1
        for p in range(pigeons):
            solver.add_clause([var(p, h) for h in range(holes)])
        for h in range(holes):
            for p1 in range(pigeons):
                for p2 in range(p1 + 1, pigeons):
                    solver.add_clause([-var(p1, h), -var(p2, h)])
        res = solver.solve()
        self.assertEqual(res.status, Status.SAT)
        self.assertTrue(res.verified)


class AssumptionTests(unittest.TestCase):
    def setUp(self):
        self.solver = Solver()
        self.solver.add_clause([1, 2])  # a or b

    def test_assumption_guides_model(self):
        res = self.solver.solve(assumptions=[-1])
        self.assertEqual(res.status, Status.SAT)
        self.assertFalse(res.model[1])
        self.assertTrue(res.model[2])

    def test_assumptions_are_retracted(self):
        res1 = self.solver.solve(assumptions=[-1, -2])
        self.assertEqual(res1.status, Status.UNSAT)
        # Solver must be back at level 0 and fully reusable.
        self.assertEqual(self.solver.decision_level, 0)
        self.assertEqual(self.solver.assign, [0, 0, 0])
        res2 = self.solver.solve()
        self.assertEqual(res2.status, Status.SAT)
        res3 = self.solver.solve(assumptions=[-2])
        self.assertEqual(res3.status, Status.SAT)
        self.assertTrue(res3.model[1])

    def test_conflicting_assumptions(self):
        self.solver.add_clause([3])
        res = self.solver.solve(assumptions=[-3])
        self.assertEqual(res.status, Status.UNSAT)
        self.assertIn("assumption", res.explanation)
        res2 = self.solver.solve()
        self.assertEqual(res2.status, Status.SAT)

    def test_state_clean_after_unsat_search(self):
        # A formula that requires real search + backtracking under assumptions.
        solver = Solver()
        for clause in ([1, 2, 3], [-1, -2], [-2, -3], [-1, -3]):
            solver.add_clause(clause)
        res_unsat = solver.solve(assumptions=[1, 2])
        self.assertEqual(res_unsat.status, Status.UNSAT)
        res_sat = solver.solve()
        self.assertEqual(res_sat.status, Status.SAT)
        self.assertTrue(res_sat.verified)
        # Watches survived the backtrack: solving again gives a verified model.
        res_sat2 = solver.solve()
        self.assertEqual(res_sat2.status, Status.SAT)
        self.assertTrue(check_model(solver.original_clauses, res_sat2.model))


class IncrementalTests(unittest.TestCase):
    def test_add_clauses_between_solves(self):
        solver = Solver()
        solver.add_clause([1, 2])
        self.assertEqual(solver.solve().status, Status.SAT)
        solver.add_clause([-1])
        res = solver.solve()
        self.assertEqual(res.status, Status.SAT)
        self.assertFalse(res.model[1])
        self.assertTrue(res.model[2])
        solver.add_clause([-2])
        self.assertEqual(solver.solve().status, Status.UNSAT)


class IndependentCheckerTests(unittest.TestCase):
    def test_checker_accepts_good_model(self):
        self.assertTrue(check_model([[1, -2], [2, 3]], {1: True, 2: True, 3: False}))

    def test_checker_rejects_bad_model(self):
        clauses = [[1, -2], [2, 3]]
        model = {1: True, 2: False, 3: False}  # violates only clause 1
        self.assertFalse(check_model(clauses, model))
        self.assertEqual(first_violated(clauses, model), 1)

    def test_every_sat_model_is_verified(self):
        solver = Solver()
        for clause in ([1, 2], [-1, 3], [-3, 4], [2, -4]):
            solver.add_clause(clause)
        res = solver.solve()
        self.assertEqual(res.status, Status.SAT)
        self.assertTrue(res.verified)
        self.assertTrue(check_model(solver.original_clauses, res.model))


class FiniteDomainTests(unittest.TestCase):
    def test_exactly_one_encoding(self):
        solver = Solver()
        enc = FiniteDomainEncoder(solver)
        enc.new_variable("x", ["r", "g", "b"])
        res = solver.solve()
        self.assertEqual(res.status, Status.SAT)
        self.assertIn(enc.decode("x", res.model), ["r", "g", "b"])
        chosen = [v for v in "rgb" if res.model[enc.literal("x", v)]]
        self.assertEqual(len(chosen), 1)

    def test_two_coloring_of_triangle_unsat(self):
        solver = Solver()
        enc = FiniteDomainEncoder(solver)
        for v in range(3):
            enc.new_variable(f"v{v}", [0, 1])
        for a, b in ((0, 1), (1, 2), (2, 0)):
            for c in (0, 1):
                solver.add_clause([-enc.literal(f"v{a}", c), -enc.literal(f"v{b}", c)])
        res = solver.solve()
        self.assertEqual(res.status, Status.UNSAT)

    def test_finite_domain_with_assumptions(self):
        solver = Solver()
        enc = FiniteDomainEncoder(solver)
        enc.new_variable("x", [1, 2, 3])
        res = solver.solve(assumptions=[enc.literal("x", 2)])
        self.assertEqual(res.status, Status.SAT)
        self.assertEqual(enc.decode("x", res.model), 2)
        res2 = solver.solve(assumptions=[-enc.literal("x", 2)])
        self.assertEqual(res2.status, Status.SAT)
        self.assertIn(enc.decode("x", res2.model), [1, 3])


class RandomizedCrossValidationTests(unittest.TestCase):
    """Compare the solver against exhaustive enumeration on random formulas."""

    def test_random_formulas_match_brute_force(self):
        rng = random.Random(20260927)
        for trial in range(300):
            n_vars = rng.randint(1, 5)
            n_clauses = rng.randint(0, 9)
            clauses = []
            solver = Solver()
            for _ in range(n_clauses):
                size = rng.randint(1, 3)
                clause = []
                for _ in range(size):
                    var = rng.randint(1, n_vars)
                    clause.append(var if rng.random() < 0.5 else -var)
                clauses.append(clause)
                solver.add_clause(clause)
            assumptions = []
            if n_vars and rng.random() < 0.5:
                var = rng.randint(1, n_vars)
                assumptions.append(var if rng.random() < 0.5 else -var)
            res = solver.solve(assumptions=assumptions)
            oracle = brute_force(clauses, n_vars, assumptions)
            if oracle is None:
                self.assertEqual(res.status, Status.UNSAT,
                                 msg=f"trial {trial}: {clauses} {assumptions}")
            else:
                self.assertEqual(res.status, Status.SAT,
                                 msg=f"trial {trial}: {clauses} {assumptions}")
                self.assertTrue(res.verified)
                self.assertTrue(check_model(clauses, res.model))
                for lit in assumptions:
                    self.assertEqual(res.model[abs(lit)], lit > 0)
            # Retraction: solver must be reusable and agree with the oracle
            # without assumptions as well.
            res_plain = solver.solve()
            oracle_plain = brute_force(clauses, n_vars)
            self.assertEqual(res_plain.status,
                             Status.SAT if oracle_plain is not None else Status.UNSAT,
                             msg=f"trial {trial} (plain): {clauses}")

    def test_random_formulas_with_empty_clause(self):
        rng = random.Random(7)
        for _ in range(50):
            solver = Solver()
            for _ in range(rng.randint(0, 5)):
                solver.add_clause([rng.randint(1, 3)])
            solver.add_clause([])
            self.assertEqual(solver.solve().status, Status.UNSAT)


if __name__ == "__main__":
    unittest.main(verbosity=2)
