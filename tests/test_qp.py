import random
import unittest
from fractions import Fraction

from qp import (InfeasibleError, Publication, check_kkt, objective,
                project_simplex, solve_by_enumeration, solve_iterative)


def leaf_pub(i, value, n, weight=Fraction(1)):
    return Publication(f"leaf:{i}", {i: Fraction(1)}, Fraction(value), weight)


def group_pub(name, members, value, weight=Fraction(1)):
    return Publication(name, {i: Fraction(1) for i in members},
                       Fraction(value), weight)


class TestSimplexProjection(unittest.TestCase):
    def test_uniform(self):
        self.assertEqual(project_simplex([0, 0, 0], 3),
                         [Fraction(1)] * 3)

    def test_negative_entries_and_zero_buckets(self):
        x = project_simplex([5, -1, -1], 3)
        self.assertEqual(x, [Fraction(3), Fraction(0), Fraction(0)])

    def test_sum_and_nonnegative(self):
        y = [Fraction(-3), Fraction(2), Fraction(7), Fraction(0)]
        x = project_simplex(y, Fraction(5))
        self.assertEqual(sum(x), 5)
        self.assertTrue(all(v >= 0 for v in x))

    def test_infeasible_negative_total(self):
        with self.assertRaises(InfeasibleError):
            project_simplex([1, 2], -1)


class TestIterativeSolver(unittest.TestCase):
    def test_infeasible_total_rejected(self):
        pubs = [leaf_pub(0, 3, 2), leaf_pub(1, 4, 2)]
        with self.assertRaises(InfeasibleError):
            solve_iterative(pubs, 2, -1)

    def test_zero_total(self):
        pubs = [leaf_pub(0, -2, 2), leaf_pub(1, 5, 2)]
        sol = solve_iterative(pubs, 2, 0)
        self.assertEqual(sol.status, "optimal")
        self.assertEqual(sol.x, [Fraction(0), Fraction(0)])
        self.assertEqual(check_kkt(pubs, 2, 0, sol.x, sol.lam, sol.mu), [])

    def test_negative_noise_becomes_zero(self):
        # Noisy values may be negative; the projection must not be.
        pubs = [leaf_pub(0, -4, 3), leaf_pub(1, 6, 3), leaf_pub(2, 1, 3)]
        sol = solve_iterative(pubs, 3, 5)
        self.assertEqual(sol.status, "optimal")
        self.assertTrue(all(v >= 0 for v in sol.x))
        self.assertEqual(sum(sol.x), 5)
        self.assertEqual(check_kkt(pubs, 3, 5, sol.x, sol.lam, sol.mu), [])

    def test_overlapping_groups_kkt(self):
        pubs = [leaf_pub(0, 4, 4), leaf_pub(1, 2, 4),
                leaf_pub(2, 1, 4), leaf_pub(3, 3, 4),
                group_pub("g01", (0, 1), 7), group_pub("g23", (2, 3), 3)]
        sol = solve_iterative(pubs, 4, 10)
        self.assertEqual(sol.status, "optimal")
        self.assertEqual(check_kkt(pubs, 4, 10, sol.x, sol.lam, sol.mu), [])

    def test_budget_exhausted_marks_not_converged(self):
        pubs = [leaf_pub(i, i + 1, 4) for i in range(4)]
        sol = solve_iterative(pubs, 4, 10, max_iterations=0)
        self.assertEqual(sol.status, "not_converged")

    def test_kkt_checker_rejects_tampering(self):
        pubs = [leaf_pub(0, 4, 3), leaf_pub(1, 2, 3), leaf_pub(2, 4, 3)]
        sol = solve_iterative(pubs, 3, 9)
        self.assertEqual(check_kkt(pubs, 3, 9, sol.x, sol.lam, sol.mu), [])
        bad_x = list(sol.x)
        bad_x[0] += 1
        bad_x[1] -= 1
        self.assertNotEqual(check_kkt(pubs, 3, 9, bad_x, sol.lam, sol.mu), [])
        bad_mu = list(sol.mu)
        bad_mu[0] += 1
        self.assertNotEqual(check_kkt(pubs, 3, 9, sol.x, sol.lam, bad_mu), [])


class TestEnumerationReference(unittest.TestCase):
    def test_matches_iterative_on_random_instances(self):
        rng = random.Random(20261001)
        for trial in range(30):
            n = rng.randint(2, 5)
            pubs = [leaf_pub(i, rng.randint(-3, 9), n) for i in range(n)]
            for g in range(rng.randint(0, 2)):
                members = tuple(sorted(rng.sample(range(n),
                                                  rng.randint(2, n))))
                pubs.append(group_pub(f"g{g}", members, rng.randint(0, 12)))
            total = rng.randint(0, 12)
            fast = solve_iterative(pubs, n, total)
            ref = solve_by_enumeration(pubs, n, total)
            self.assertEqual(fast.status, "optimal")
            self.assertEqual(fast.x, ref.x, f"trial {trial}")
            self.assertEqual(fast.objective_value, ref.objective_value)
            self.assertEqual(
                check_kkt(pubs, n, total, ref.x, ref.lam, ref.mu), [])

    def test_enumeration_rejects_infeasible(self):
        pubs = [leaf_pub(0, 1, 1)]
        with self.assertRaises(InfeasibleError):
            solve_by_enumeration(pubs, 1, -2)


if __name__ == "__main__":
    unittest.main()
