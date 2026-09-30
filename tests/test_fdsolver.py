import itertools
import random
import unittest

from fdsolver import Problem, Status, solve
from checker import check_assignment


def brute_force(problem):
    """Exact reference: enumerate the full Cartesian product of domains."""
    names = problem.variable_names
    best_obj = None
    best_key = None
    best_assign = None
    for values in itertools.product(*(problem.domains[n] for n in names)):
        cand = dict(zip(names, values))
        report = check_assignment(problem, cand)
        if not report.ok:
            continue
        key = tuple(cand[n] for n in names)
        if best_obj is None or report.objective < best_obj or (
                report.objective == best_obj and key < best_key):
            best_obj, best_key, best_assign = report.objective, key, cand
    return best_obj, best_assign


def random_problem(rng, n_vars=5, n_cons=4, lo=-3, hi=3):
    domains = {f"x{i}": list(range(rng.randint(0, 2), rng.randint(3, 5)))
               for i in range(n_vars)}
    objective = {f"x{i}": rng.randint(lo, hi) for i in range(n_vars)}
    constraints = []
    for _ in range(n_cons):
        coeffs = {f"x{i}": rng.randint(-2, 2)
                  for i in rng.sample(range(n_vars), rng.randint(1, n_vars))}
        sense = rng.choice(["<=", ">=", "=="])
        rhs = rng.randint(-6, 10)
        constraints.append((coeffs, sense, rhs))
    return Problem(domains, objective, constraints)


class TestBasicSolving(unittest.TestCase):
    def test_small_optimal(self):
        # min 3x + 2y  s.t.  x + y >= 4,  x,y in [0,5]  ->  (0,4), obj 8
        p = Problem({"x": range(6), "y": range(6)},
                    {"x": 3, "y": 2},
                    [({"x": 1, "y": 1}, ">=", 4)])
        r = solve(p)
        self.assertEqual(r.status, Status.OPTIMAL)
        self.assertEqual(r.objective_value, 8)
        self.assertEqual(r.assignment, {"x": 0, "y": 4})
        self.assertEqual(r.lower_bound, 8)
        self.assertEqual(r.absolute_gap, 0)
        self.assertTrue(r.verified)

    def test_proven_infeasible(self):
        p = Problem({"x": range(4), "y": range(4)},
                    {"x": 1},
                    [({"x": 1, "y": 1}, "<=", 1),
                     ({"x": 1, "y": 1}, ">=", 3)])
        r = solve(p)
        self.assertEqual(r.status, Status.INFEASIBLE)
        self.assertIsNone(r.assignment)
        self.assertIsNone(r.objective_value)

    def test_empty_domain_is_infeasible(self):
        p = Problem({"x": []}, {"x": 1})
        self.assertEqual(solve(p).status, Status.INFEASIBLE)

    def test_equality_and_ge_constraints(self):
        # x - y == 2, x + y >= 4, domains 0..6 -> min x+y is 4 at (3,1)
        p = Problem({"x": range(7), "y": range(7)},
                    {"x": 1, "y": 1},
                    [({"x": 1, "y": -1}, "==", 2),
                     ({"x": 1, "y": 1}, ">=", 4)])
        r = solve(p)
        self.assertEqual(r.status, Status.OPTIMAL)
        self.assertEqual(r.objective_value, 4)
        self.assertEqual(r.assignment, {"x": 3, "y": 1})

    def test_noncontiguous_domain(self):
        p = Problem({"x": [1, 4, 7]}, {"x": 1}, [({"x": 1}, ">=", 3)])
        r = solve(p)
        self.assertEqual(r.status, Status.OPTIMAL)
        self.assertEqual(r.assignment, {"x": 4})

    def test_negative_coefficients(self):
        # min -2x + y, x,y in 0..3 -> (-2*3 + 0) = -6 at (3,0)
        p = Problem({"x": range(4), "y": range(4)}, {"x": -2, "y": 1})
        r = solve(p)
        self.assertEqual(r.status, Status.OPTIMAL)
        self.assertEqual(r.objective_value, -6)
        self.assertEqual(r.assignment, {"x": 3, "y": 0})


class TestTieBreaking(unittest.TestCase):
    def test_lexicographically_smallest_optimum(self):
        # objective only involves x; constraint forces x >= 1.
        # Optima: x=1, y anything -> lexicographically smallest is y=0.
        p = Problem({"x": range(4), "y": range(4)},
                    {"x": 1},
                    [({"x": 1}, ">=", 1)])
        r = solve(p)
        self.assertEqual(r.status, Status.OPTIMAL)
        self.assertEqual(r.assignment, {"x": 1, "y": 0})

    def test_variable_order_defines_tie_break(self):
        # y declared first: tie-break prefers smallest y.
        p = Problem({"y": range(4), "x": range(4)},
                    {"x": 1},
                    [({"x": 1}, ">=", 1)])
        r = solve(p)
        self.assertEqual(r.assignment, {"y": 0, "x": 1})

    def test_deterministic_across_runs(self):
        rng = random.Random(7)
        p = random_problem(rng)
        first = solve(p)
        for _ in range(3):
            again = solve(p)
            self.assertEqual(again.assignment, first.assignment)
            self.assertEqual(again.objective_value, first.objective_value)


class TestBudgetAndBounds(unittest.TestCase):
    def test_zero_budget_is_unknown_not_infeasible(self):
        p = Problem({"x": range(5)}, {"x": 1})
        r = solve(p, max_nodes=0)
        self.assertEqual(r.status, Status.UNKNOWN)
        self.assertIsNone(r.assignment)
        # A valid lower bound is still reported and must not exceed the
        # true optimum (which is 0 here).
        self.assertIsNotNone(r.lower_bound)
        self.assertLessEqual(r.lower_bound, 0)

    def test_interrupted_returns_incumbent_with_gap(self):
        rng = random.Random(11)
        p = full = None
        for _ in range(50):  # find a feasible random instance
            candidate = random_problem(rng, n_vars=10, n_cons=3)
            full = solve(candidate)
            if full.status is Status.OPTIMAL:
                p = candidate
                break
        self.assertIsNotNone(p, "no feasible random instance generated")
        r = solve(p, max_nodes=3)
        self.assertIn(r.status, (Status.FEASIBLE, Status.UNKNOWN))
        self.assertNotEqual(r.status, Status.OPTIMAL)
        if r.status is Status.FEASIBLE:
            self.assertTrue(r.verified)
            self.assertTrue(check_assignment(p, r.assignment).ok)
            self.assertLessEqual(r.lower_bound, r.objective_value)
            self.assertEqual(r.absolute_gap,
                             r.objective_value - r.lower_bound)
            self.assertGreaterEqual(r.objective_value, full.objective_value)

    def test_time_budget(self):
        rng = random.Random(3)
        p = None
        for _ in range(50):  # find a feasible random instance
            candidate = random_problem(rng, n_vars=12, n_cons=4)
            if solve(candidate).status is Status.OPTIMAL:
                p = candidate
                break
        self.assertIsNotNone(p, "no feasible random instance generated")
        r = solve(p, max_seconds=0.0)
        self.assertIn(r.status, (Status.FEASIBLE, Status.UNKNOWN))
        self.assertNotEqual(r.status, Status.OPTIMAL)


class TestIndependentChecker(unittest.TestCase):
    def setUp(self):
        self.p = Problem({"x": range(4), "y": range(4)},
                         {"x": 2, "y": 1},
                         [({"x": 1, "y": 1}, ">=", 3)])

    def test_checker_accepts_valid(self):
        rep = check_assignment(self.p, {"x": 1, "y": 2})
        self.assertTrue(rep.ok)
        self.assertEqual(rep.objective, 4)

    def test_checker_rejects_constraint_violation(self):
        rep = check_assignment(self.p, {"x": 1, "y": 1})
        self.assertFalse(rep.ok)
        self.assertTrue(any("constraint" in v for v in rep.violations))

    def test_checker_rejects_out_of_domain(self):
        rep = check_assignment(self.p, {"x": 9, "y": 0})
        self.assertFalse(rep.ok)
        self.assertTrue(any("domain" in v for v in rep.violations))

    def test_checker_rejects_missing_and_unknown_variables(self):
        self.assertFalse(check_assignment(self.p, {"x": 3}).ok)
        self.assertFalse(check_assignment(self.p, {"x": 3, "y": 0, "z": 1}).ok)

    def test_solver_results_always_pass_checker(self):
        rng = random.Random(23)
        for _ in range(30):
            p = random_problem(rng)
            r = solve(p)
            if r.assignment is not None:
                self.assertTrue(r.verified)
                self.assertTrue(check_assignment(p, r.assignment).ok)


class TestAgainstBruteForce(unittest.TestCase):
    def test_matches_brute_force_on_random_problems(self):
        rng = random.Random(42)
        for trial in range(60):
            p = random_problem(rng)
            bf_obj, bf_assign = brute_force(p)
            r = solve(p)
            if bf_obj is None:
                self.assertEqual(r.status, Status.INFEASIBLE,
                                 f"trial {trial}: solver must prove infeasible")
            else:
                self.assertEqual(r.status, Status.OPTIMAL)
                self.assertEqual(r.objective_value, bf_obj,
                                 f"trial {trial}: objective mismatch")
                self.assertEqual(r.assignment, bf_assign,
                                 f"trial {trial}: not the lexicographically "
                                 f"smallest optimum")

    def test_lower_bound_never_exceeds_true_optimum(self):
        rng = random.Random(99)
        for trial in range(40):
            p = random_problem(rng, n_vars=6, n_cons=4)
            bf_obj, _ = brute_force(p)
            for budget in (0, 1, 2, 5, 20):
                r = solve(p, max_nodes=budget)
                if r.lower_bound is not None and bf_obj is not None:
                    self.assertLessEqual(
                        r.lower_bound, bf_obj,
                        f"trial {trial} budget {budget}: lower bound "
                        f"{r.lower_bound} exceeds true optimum {bf_obj}")
                if r.status is Status.FEASIBLE:
                    self.assertLessEqual(r.lower_bound, r.objective_value)
                    self.assertGreaterEqual(r.objective_value, bf_obj)
                # A budget-limited run must never claim optimality unless
                # the search actually finished.
                if r.status is Status.OPTIMAL:
                    self.assertEqual(r.objective_value, bf_obj)


if __name__ == "__main__":
    unittest.main()
