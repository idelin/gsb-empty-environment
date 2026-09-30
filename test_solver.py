"""Unit tests for the integer bound-propagation solver.

The RandomisedCrossCheck suite verifies EVERY pruning the solver makes
against exact brute-force enumeration over the original constraints.
"""

import random
import unittest

from checker import check_assignment, enumerate_solutions
from solver import Contradiction, Solver, ceil_div


class RoundingTests(unittest.TestCase):
    def test_ceil_div_both_signs(self):
        self.assertEqual(ceil_div(3, -2), -1)    # ceil(-1.5)
        self.assertEqual(ceil_div(-3, -2), 2)    # ceil(1.5)
        self.assertEqual(ceil_div(-3, 2), -1)    # ceil(-1.5)
        self.assertEqual(ceil_div(4, 2), 2)
        self.assertEqual(ceil_div(5, 2), 3)
        self.assertEqual(ceil_div(-4, 2), -2)
        self.assertEqual(ceil_div(0, 7), 0)

    def test_floor_division_is_exact_for_negatives(self):
        # Python // floors; the solver relies on this for upper bounds.
        self.assertEqual(-7 // 2, -4)
        self.assertEqual(7 // -2, -4)


class RequiredExamples(unittest.TestCase):
    def test_2x_le_3_gives_x_le_1(self):
        s = Solver()
        s.add_var("x", -10, 10)
        s.add_le({"x": 2}, 3)
        self.assertEqual(s.domain("x"), (-10, 1))

    def test_neg2x_le_3_gives_x_ge_neg1(self):
        s = Solver()
        s.add_var("x", -10, 10)
        s.add_le({"x": -2}, 3)
        self.assertEqual(s.domain("x"), (-1, 10))

    def test_zero_coefficient_does_not_affect_domain(self):
        s = Solver()
        s.add_var("x", -10, 10)
        s.add_var("y", -10, 10)
        s.add_le({"x": 0, "y": 1}, 5)
        self.assertEqual(s.domain("x"), (-10, 10))  # untouched
        self.assertEqual(s.domain("y"), (-10, 5))

    def test_constant_contradiction_fails_immediately(self):
        s = Solver()
        s.add_var("x", -10, 10)
        with self.assertRaises(Contradiction):
            s.add_le({"x": 0}, -1)   # 0 <= -1 false
        with self.assertRaises(Contradiction):
            s.add_ge({"x": 0}, 1)    # 0 >= 1 false
        with self.assertRaises(Contradiction):
            s.add_eq({}, 2)          # 0 == 2 false
        # constant-true constraints are accepted and change nothing
        s.add_le({"x": 0}, 0)
        s.add_eq({}, 0)
        self.assertEqual(s.domain("x"), (-10, 10))


class NegativeCoefficientRounding(unittest.TestCase):
    def _run(self, coeffs, sense, rhs, lo=-100, hi=100):
        s = Solver()
        s.add_var("x", lo, hi)
        s.add(coeffs, sense, rhs)
        return s.domain("x")

    def test_lower_bounds_via_ceil(self):
        self.assertEqual(self._run({"x": -2}, "<=", 3), (-1, 100))
        self.assertEqual(self._run({"x": -2}, "<=", -3), (2, 100))
        self.assertEqual(self._run({"x": -3}, "<=", 7), (-2, 100))
        self.assertEqual(self._run({"x": -1}, ">=", -5), (-100, 5))

    def test_upper_bounds_via_floor(self):
        self.assertEqual(self._run({"x": 3}, "<=", -7), (-100, -3))
        self.assertEqual(self._run({"x": 2}, "<=", 5), (-100, 2))
        self.assertEqual(self._run({"x": -2}, ">=", 3), (-100, -2))
        self.assertEqual(self._run({"x": 2}, ">=", 3), (2, 100))

    def test_exact_division_no_off_by_one(self):
        self.assertEqual(self._run({"x": 2}, "<=", 4), (-100, 2))
        self.assertEqual(self._run({"x": -2}, "<=", -4), (2, 100))


class EqualityAndCongruence(unittest.TestCase):
    def test_equality_splits_both_directions(self):
        s = Solver()
        s.add_var("x", 0, 10)
        s.add_var("y", 0, 10)
        s.add_eq({"x": 1, "y": 1}, 4)
        self.assertEqual(s.domain("x"), (0, 4))   # upper from LE form
        self.assertEqual(s.domain("y"), (0, 4))
        s.add_var("z", 0, 10)
        s.add_eq({"z": 1, "y": 1}, 8)
        self.assertEqual(s.domain("z"), (4, 8))   # lower bound from GE form

    def test_congruence_catches_interval_invisible_infeasibility(self):
        # 2x+2y+2z == 3 over [0,1]^3: interval bounds derive nothing,
        # but gcd(2,2,2)=2 does not divide 3.
        s = Solver()
        for v in "xyz":
            s.add_var(v, 0, 1)
        with self.assertRaises(Contradiction):
            s.add_eq({"x": 2, "y": 2, "z": 2}, 3)

    def test_congruence_with_negative_coefficients(self):
        s = Solver()
        s.add_var("x", -9, 9)
        s.add_var("y", -9, 9)
        with self.assertRaises(Contradiction):
            s.add_eq({"x": 4, "y": -6}, 3)  # gcd(4,6)=2 does not divide 3

    def test_propagation_is_not_complete_and_does_not_claim_to_be(self):
        # x+y==1 and x-y==0 over [0,1] force 2x==1: no integer solution,
        # yet neither interval bounds nor the gcd test can see it, so
        # propagate() must stay quiet (no false UNSAT/CAT claim).
        s = Solver()
        s.add_var("x", 0, 1)
        s.add_var("y", 0, 1)
        s.add_eq({"x": 1, "y": 1}, 1)   # must NOT raise
        s.add_eq({"x": 1, "y": -1}, 0)  # must NOT raise
        self.assertEqual(s.domain("x"), (0, 1))
        self.assertEqual(s.domain("y"), (0, 1))
        # The complete search (branching) does prove it unsatisfiable.
        self.assertIsNone(s.solve())
        specs = [({"x": 1, "y": 1}, "==", 1), ({"x": 1, "y": -1}, "==", 0)]
        self.assertEqual(
            enumerate_solutions({"x": (0, 1), "y": (0, 1)}, specs), [])


class BigIntegers(unittest.TestCase):
    def test_huge_coefficients_no_overflow(self):
        s = Solver()
        s.add_var("x", -(10**60), 10**60)
        s.add_le({"x": 10**50}, 3 * 10**50 + 7)
        self.assertEqual(s.domain("x"), (-(10**60), 3))
        s.add_le({"x": -(10**50)}, 2 * 10**50 + 3)
        self.assertEqual(s.domain("x"), (-2, 3))

    def test_huge_products_and_mixed_signs(self):
        s = Solver()
        s.add_var("x", -(10**40), 10**40)
        s.add_var("y", 0, 10**40)
        # -(10^40) x + (10^40) y <= 10^40  with 0 <= y <= 2 forces x >= -1
        s.add_le({"y": 1}, 2)
        s.add_le({"x": -(10**40), "y": 10**40}, 10**40)
        self.assertEqual(s.domain("x")[0], -1)


class QueueFixpointAndExplanations(unittest.TestCase):
    def test_shared_variables_propagate_to_fixpoint(self):
        s = Solver()
        for v in "xyz":
            s.add_var(v, 0, 10)
        s.add_le({"x": 1, "y": -1}, -1)   # x <= y - 1
        s.add_le({"y": 1, "z": -1}, -1)   # y <= z - 1
        s.add_le({"z": 1}, 5)
        self.assertEqual(s.domain("z"), (2, 5))
        self.assertEqual(s.domain("y"), (1, 4))
        self.assertEqual(s.domain("x"), (0, 3))

    def test_explanations_record_the_causing_constraint(self):
        s = Solver()
        s.add_var("x", 0, 10)
        s.add_var("y", 0, 10)
        cid = s.add_le({"x": 1, "y": 1}, 4)
        self.assertIn("x", s.explanations)
        self.assertEqual(s.explanations["x"]["constraint"], cid)
        self.assertEqual(s.explanations["x"]["new"], 4)
        self.assertTrue(any(e["var"] == "y" for e in s.prune_log))

    def test_conflict_reports_explanation(self):
        s = Solver()
        s.add_var("x", 0, 3)
        with self.assertRaises(Contradiction) as ctx:
            s.add_ge({"x": 1}, 5)
        self.assertIn("x", ctx.exception.explanation)


class RetractAndBacktrack(unittest.TestCase):
    def test_retract_restores_domains(self):
        s = Solver()
        s.add_var("x", 0, 10)
        s.add_var("y", 0, 10)
        s.add_le({"x": 1, "y": 1}, 8)
        cid = s.add_ge({"x": 1}, 5)
        self.assertEqual(s.domain("x"), (5, 8))
        self.assertEqual(s.domain("y"), (0, 3))   # via x >= 5, x + y <= 8
        s.retract(cid)
        self.assertEqual(s.domain("x"), (0, 8))
        self.assertEqual(s.domain("y"), (0, 8))

    def test_retract_keeps_remaining_constraints_sound(self):
        s = Solver()
        s.add_var("x", 0, 10)
        c1 = s.add_le({"x": 1}, 7)
        c2 = s.add_le({"x": 1}, 3)
        s.retract(c2)
        self.assertEqual(s.domain("x"), (0, 7))
        s.retract(c1)
        self.assertEqual(s.domain("x"), (0, 10))

    def test_checkpoint_backtrack_restores_domains(self):
        s = Solver()
        s.add_var("x", 0, 10)
        cp = s.checkpoint()
        s.add_le({"x": 1}, 3)
        self.assertEqual(s.domain("x"), (0, 3))
        s.backtrack(cp)
        self.assertEqual(s.domain("x"), (0, 10))


class SolveAndIndependentCheck(unittest.TestCase):
    def test_solution_verified_by_independent_checker(self):
        s = Solver()
        s.add_var("x", -5, 5)
        s.add_var("y", -5, 5)
        s.add_le({"x": 2, "y": 1}, 4)
        s.add_ge({"x": 1, "y": -1}, 0)
        sol = s.solve()
        self.assertIsNotNone(sol)
        self.assertTrue(check_assignment(s.active_specs(), sol))

    def test_unsat_matches_enumeration(self):
        s = Solver()
        s.add_var("x", 0, 4)
        s.add_var("y", 0, 4)
        specs = [({"x": 1, "y": 1}, ">=", 9)]  # max achievable sum is 8
        with self.assertRaises(Contradiction):
            s.add_ge({"x": 1, "y": 1}, 9)
        self.assertEqual(
            enumerate_solutions({"x": (0, 4), "y": (0, 4)}, specs), [])
        # The failed add was rolled back: no active constraints remain.
        self.assertEqual(s.active_specs(), [])


class RandomisedCrossCheck(unittest.TestCase):
    """Every pruning is checked against exact reference enumeration."""

    def _random_instance(self, rng):
        nvars = rng.randint(1, 4)
        names = [f"x{i}" for i in range(nvars)]
        domains = {}
        for n in names:
            lo = rng.randint(-4, 1)
            hi = lo + rng.randint(0, 4)
            domains[n] = (lo, hi)
        specs = []
        for _ in range(rng.randint(1, 5)):
            coeffs = {n: rng.randint(-3, 3) for n in names}
            sense = rng.choice(["<=", ">=", "=="])
            rhs = rng.randint(-10, 10)
            specs.append((coeffs, sense, rhs))
        return names, domains, specs

    def test_every_prune_is_exact_and_conflicts_are_real(self):
        rng = random.Random(20261001)
        for trial in range(300):
            names, domains, specs = self._random_instance(rng)
            s = Solver()
            for n in names:
                s.add_var(n, *domains[n])

            pruned = []  # (var, value) removed by propagation

            def on_prune(name, old_lo, old_hi, new_lo, new_hi, _expl):
                for v in range(old_lo, new_lo):
                    pruned.append((name, v))
                for v in range(new_hi + 1, old_hi + 1):
                    pruned.append((name, v))

            s.on_prune = on_prune
            conflict = False
            kept_specs = []
            for coeffs, sense, rhs in specs:
                kept_specs.append((coeffs, sense, rhs))
                try:
                    s.add(coeffs, sense, rhs)
                except Contradiction:
                    conflict = True
                    break

            solutions = enumerate_solutions(domains, kept_specs)

            # 1. No pruned value may appear in any exact solution.
            for var, value in pruned:
                for sol in solutions:
                    self.assertNotEqual(
                        sol[var], value,
                        f"trial {trial}: solver pruned {var}={value} "
                        f"but {sol} is a solution of {kept_specs}")

            # 2. A derived contradiction must be a real infeasibility.
            if conflict:
                self.assertEqual(
                    solutions, [],
                    f"trial {trial}: solver reported contradiction but "
                    f"{solutions[:1]} satisfies {kept_specs}")

    def test_solve_agrees_with_enumeration(self):
        rng = random.Random(777)
        for trial in range(150):
            names, domains, specs = self._random_instance(rng)
            s = Solver()
            for n in names:
                s.add_var(n, *domains[n])
            try:
                for coeffs, sense, rhs in specs:
                    s.add(coeffs, sense, rhs)
            except Contradiction:
                self.assertEqual(
                    enumerate_solutions(domains, specs), [],
                    f"trial {trial}: false contradiction on {specs}")
                continue
            result = s.solve()
            solutions = enumerate_solutions(domains, specs)
            if result is None:
                self.assertEqual(solutions, [],
                                 f"trial {trial}: solve() missed {solutions[:1]}")
            else:
                self.assertTrue(check_assignment(specs, result),
                                f"trial {trial}: bad solution {result}")
                self.assertTrue(solutions)


if __name__ == "__main__":
    unittest.main(verbosity=2)
