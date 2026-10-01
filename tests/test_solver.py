"""unittest suite for the finite-domain branch-and-bound solver.

Run with:  python3.11 -m unittest discover -s tests -v
"""
from __future__ import annotations

import itertools
import random
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from checker import check_assignment, verify_solution
from model import LinearConstraint, Problem, Sense
from solver import Status, solve


def brute_force(problem: Problem):
    """Exhaustive reference: (objective, assignment) of the lexicographically
    smallest optimal solution, or None if the problem is infeasible."""
    best = None
    for values in itertools.product(*problem.domains):
        if check_assignment(problem, values):
            continue
        obj = sum(c * x for c, x in zip(problem.objective, values))
        key = (obj, values)
        if best is None or key < best:
            best = key
    return best


def random_problem(rng: random.Random) -> Problem:
    n = rng.randint(1, 5)
    domains = []
    for _ in range(n):
        lo = rng.randint(-3, 2)
        hi = lo + rng.randint(0, 4)
        domains.append(list(range(lo, hi + 1)))
    objective = [rng.randint(-4, 5) for _ in range(n)]
    constraints = []
    for _ in range(rng.randint(0, 4)):
        coeffs = [rng.randint(-3, 3) for _ in range(n)]
        sense = rng.choice([Sense.LE, Sense.GE, Sense.EQ])
        rhs = rng.randint(-8, 8)
        constraints.append(LinearConstraint(coeffs, sense, rhs))
    return Problem(domains, objective, constraints)


class TestAgainstBruteForce(unittest.TestCase):
    def test_random_problems_match_brute_force(self):
        rng = random.Random(20261001)
        for trial in range(300):
            problem = random_problem(rng)
            result = solve(problem)
            reference = brute_force(problem)
            with self.subTest(trial=trial):
                if reference is None:
                    self.assertEqual(result.status, Status.INFEASIBLE)
                    self.assertIsNone(result.solution)
                    self.assertIsNone(result.lower_bound)
                    self.assertIsNone(result.upper_bound)
                else:
                    ref_obj, ref_values = reference
                    self.assertEqual(result.status, Status.OPTIMAL)
                    self.assertEqual(result.objective, ref_obj)
                    self.assertEqual(result.solution, ref_values)
                    self.assertEqual(result.lower_bound, ref_obj)
                    self.assertEqual(result.upper_bound, ref_obj)
                    self.assertEqual(result.gap, 0)

    def test_fixed_problem(self):
        problem = Problem(
            domains=[[0, 1, 2], [0, 1, 2]],
            objective=(1, 2),
            constraints=[LinearConstraint((1, 1), Sense.GE, 2)],
        )
        result = solve(problem)
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.solution, (2, 0))
        self.assertEqual(result.objective, 2)


class TestStatuses(unittest.TestCase):
    def test_proven_infeasible(self):
        problem = Problem(
            domains=[[0, 1, 2, 3]],
            objective=(1,),
            constraints=[LinearConstraint((1,), Sense.GE, 5)],
        )
        result = solve(problem)
        self.assertEqual(result.status, Status.INFEASIBLE)
        self.assertFalse(result.interrupted)
        self.assertIsNone(result.solution)

    def test_unknown_vs_infeasible(self):
        problem = Problem(
            domains=[range(0, 8)] * 5,
            objective=(1,) * 5,
            constraints=[LinearConstraint((1,) * 5, Sense.GE, 25)],
        )
        result = solve(problem, max_nodes=2)
        self.assertEqual(result.status, Status.UNKNOWN)
        self.assertTrue(result.interrupted)
        self.assertIsNone(result.solution)
        self.assertIsNone(result.upper_bound)
        self.assertIsNotNone(result.lower_bound)
        full = solve(problem)
        self.assertEqual(full.status, Status.OPTIMAL)

    def test_interrupted_with_incumbent_is_feasible_not_optimal(self):
        problem = Problem(
            domains=[range(0, 10)] * 10,
            objective=tuple(range(1, 11)),
            constraints=[LinearConstraint((1,) * 10, Sense.GE, 20)],
        )
        result = solve(problem, max_nodes=50)
        self.assertTrue(result.interrupted)
        self.assertEqual(result.status, Status.FEASIBLE)
        self.assertIsNotNone(result.solution)
        self.assertNotEqual(result.status, Status.OPTIMAL)
        self.assertGreater(result.gap, -1)


class TestBoundsAndGap(unittest.TestCase):
    def test_lower_bound_never_exceeds_true_optimum(self):
        rng = random.Random(777)
        for trial in range(200):
            problem = random_problem(rng)
            reference = brute_force(problem)
            budget = rng.randint(1, 30)
            result = solve(problem, max_nodes=budget)
            with self.subTest(trial=trial):
                if result.lower_bound is not None and reference is not None:
                    self.assertLessEqual(result.lower_bound, reference[0])
                if result.upper_bound is not None and reference is not None:
                    self.assertLessEqual(reference[0], result.upper_bound)
                if result.lower_bound is not None and result.upper_bound is not None:
                    self.assertLessEqual(result.lower_bound, result.upper_bound)
                    self.assertEqual(
                        result.gap, result.upper_bound - result.lower_bound
                    )

    def test_time_limit_interruption(self):
        problem = Problem(
            domains=[range(0, 8)] * 14,
            objective=tuple(range(14)),
            constraints=[LinearConstraint((1,) * 14, Sense.GE, 40)],
        )
        result = solve(problem, time_limit=0.0)
        self.assertTrue(result.interrupted)
        self.assertIn(result.status, (Status.FEASIBLE, Status.UNKNOWN))
        if result.solution is not None:
            ok, obj = verify_solution(problem, result.solution)
            self.assertTrue(ok)
            self.assertEqual(obj, result.objective)


class TestTieBreak(unittest.TestCase):
    def test_lexicographically_smallest_among_equal_objective(self):
        problem = Problem(
            domains=[[1, 2], [0, 1]],
            objective=(0, 0),
            constraints=[LinearConstraint((1, 1), Sense.GE, 1)],
        )
        result = solve(problem)
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.solution, (1, 0))

    def test_tie_break_with_negative_domains(self):
        problem = Problem(
            domains=[[-2, -1, 0], [-1, 0, 1]],
            objective=(1, -1),
            constraints=[LinearConstraint((1, 1), Sense.EQ, -1)],
        )
        result = solve(problem)
        self.assertEqual(result.solution, (-2, 1))
        self.assertEqual(result.objective, -3)


class TestIndependentChecker(unittest.TestCase):
    def test_checker_accepts_feasible(self):
        problem = Problem(
            domains=[[0, 1], [0, 1]],
            objective=(1, 1),
            constraints=[LinearConstraint((1, 1), Sense.LE, 1)],
        )
        self.assertEqual(check_assignment(problem, (0, 1)), [])
        ok, obj = verify_solution(problem, (0, 1))
        self.assertTrue(ok)
        self.assertEqual(obj, 1)

    def test_checker_rejects_domain_violation(self):
        problem = Problem(domains=[[0, 1]], objective=(1,))
        errors = check_assignment(problem, (2,))
        self.assertTrue(errors)
        ok, obj = verify_solution(problem, (2,))
        self.assertFalse(ok)
        self.assertIsNone(obj)

    def test_checker_rejects_constraint_violation(self):
        problem = Problem(
            domains=[[0, 5]],
            objective=(1,),
            constraints=[LinearConstraint((1,), Sense.LE, 3)],
        )
        self.assertTrue(check_assignment(problem, (5,)))
        ok, _ = verify_solution(problem, (5,))
        self.assertFalse(ok)

    def test_checker_rejects_wrong_arity(self):
        problem = Problem(domains=[[0], [0]], objective=(1, 1))
        self.assertTrue(check_assignment(problem, (0,)))

    def test_every_returned_solution_passes_independent_check(self):
        rng = random.Random(42)
        for _ in range(100):
            problem = random_problem(rng)
            result = solve(problem)
            if result.solution is not None:
                ok, obj = verify_solution(problem, result.solution)
                self.assertTrue(ok)
                self.assertEqual(obj, result.objective)


class TestConstraintKinds(unittest.TestCase):
    def test_equality_constraint(self):
        problem = Problem(
            domains=[range(0, 5), range(0, 5)],
            objective=(2, 1),
            constraints=[LinearConstraint((1, 1), Sense.EQ, 3)],
        )
        result = solve(problem)
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.solution, (0, 3))
        self.assertEqual(result.objective, 3)

    def test_negative_coefficients(self):
        problem = Problem(
            domains=[[0, 1, 2, 3]],
            objective=(-2,),
            constraints=[LinearConstraint((-1,), Sense.GE, -2)],
        )
        result = solve(problem)
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.solution, (2,))
        self.assertEqual(result.objective, -4)


if __name__ == "__main__":
    unittest.main()
