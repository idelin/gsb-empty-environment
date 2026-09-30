import unittest
from fractions import Fraction

from dpproj.qp import InfeasibleError, solve_qp, verify_kkt
from dpproj.reference import solve_qp_enumeration


class TestProjectionBasics(unittest.TestCase):
    def test_simplex_projection_with_clamping(self):
        # y = (-1, 2, 3), total 5: optimum is (0, 2, 3).
        r = solve_qp([1, 1, 1], [-1, 2, 3], [[1, 1, 1]], [5])
        self.assertEqual(r.status, "ok")
        self.assertEqual(r.x, [Fraction(0), Fraction(2), Fraction(3)])
        self.assertEqual(sum(r.x), 5)
        self.assertTrue(all(v >= 0 for v in r.x))

    def test_weighted_projection_closed_form(self):
        # min x1^2 + 4 x2^2 s.t. x1 + x2 = 4 -> (16/5, 4/5).
        r = solve_qp([1, 4], [0, 0], [[1, 1]], [4])
        self.assertEqual(r.x, [Fraction(16, 5), Fraction(4, 5)])

    def test_rational_weights(self):
        w = [Fraction(1, 3), Fraction(7, 2), Fraction(2, 5)]
        y = [Fraction(-2), Fraction(5), Fraction(1)]
        r = solve_qp(w, y, [[1, 1, 1]], [Fraction(9, 2)])
        self.assertEqual(r.status, "ok")
        self.assertEqual(sum(r.x), Fraction(9, 2))
        self.assertTrue(verify_kkt(w, y, [[1, 1, 1]],
                                   [Fraction(9, 2)], r.certificate))

    def test_no_equalities_clamps_at_zero(self):
        r = solve_qp([1, 1], [3, -4], [], [])
        self.assertEqual(r.x, [Fraction(3), Fraction(0)])

    def test_zero_vector_target(self):
        r = solve_qp([1, 1, 1], [1, -2, 3], [[1, 1, 1]], [0])
        self.assertEqual(r.x, [Fraction(0)] * 3)

    def test_negative_noise_and_zero_buckets(self):
        # All-zero counts with negative noisy values must project to a
        # non-negative vector matching the (non-negative) total.
        r = solve_qp([1, 1, 1], [-5, -1, -2], [[1, 1, 1]], [2])
        self.assertEqual(r.status, "ok")
        self.assertEqual(sum(r.x), 2)
        self.assertTrue(all(v >= 0 for v in r.x))


class TestInfeasibilityAndBudget(unittest.TestCase):
    def test_negative_total_rejected(self):
        with self.assertRaises(InfeasibleError):
            solve_qp([1, 1, 1], [1, 2, 3], [[1, 1, 1]], [-1])

    def test_contradictory_constraints_rejected(self):
        # x1 + x2 = 2 and x1 + x2 = 3 cannot both hold.
        with self.assertRaises(InfeasibleError):
            solve_qp([1, 1], [0, 0], [[1, 1], [1, 1]], [2, 3])

    def test_impossible_group_sum_rejected(self):
        # x1 = 2 (row 1) but also x1 + x2 = 1 with x >= 0 is fine;
        # x1 = 2 and x1 + x2 = 1 is impossible.
        with self.assertRaises(InfeasibleError):
            solve_qp([1, 1], [0, 0], [[1, 0], [1, 1]], [2, 1])

    def test_iteration_budget_marks_not_converged(self):
        r = solve_qp([1, 1, 1], [-1, 2, 3], [[1, 1, 1]], [5],
                     max_iterations=1)
        self.assertFalse(r.converged)
        self.assertEqual(r.status, "not_converged")
        self.assertIsNone(r.certificate)


class TestKKT(unittest.TestCase):
    def test_certificate_verifies(self):
        w = [Fraction(2), Fraction(1), Fraction(3)]
        y = [Fraction(-1), Fraction(4), Fraction(0)]
        A = [[Fraction(1), Fraction(1), Fraction(1)]]
        b = [Fraction(6)]
        r = solve_qp(w, y, A, b)
        self.assertTrue(verify_kkt(w, y, A, b, r.certificate))

    def test_tampered_certificate_rejected(self):
        w = [1, 1]
        y = [0, 0]
        A = [[1, 1]]
        b = [4]
        r = solve_qp(w, y, A, b)
        bad = type(r.certificate)(x=[Fraction(3), Fraction(1)],
                                  lam=r.certificate.lam,
                                  mu=r.certificate.mu)
        self.assertFalse(verify_kkt(w, y, A, b, bad))
        bad2 = type(r.certificate)(x=r.certificate.x,
                                   lam=r.certificate.lam,
                                   mu=[Fraction(-1), Fraction(0)])
        self.assertFalse(verify_kkt(w, y, A, b, bad2))

    def test_overlapping_constraints(self):
        # Two overlapping group sums: x1+x2 = 3, x2+x3 = 4, total = 6.
        A = [[1, 1, 0], [0, 1, 1], [1, 1, 1]]
        b = [3, 4, 6]
        w = [1, 1, 1]
        y = [0, 5, 0]
        r = solve_qp(w, y, A, b)
        self.assertEqual(r.status, "ok")
        self.assertTrue(verify_kkt(w, y, A, b, r.certificate))
        self.assertEqual(r.x[0] + r.x[1], 3)
        self.assertEqual(r.x[1] + r.x[2], 4)


class TestEnumerationReference(unittest.TestCase):
    def test_matches_solver_on_deterministic_cases(self):
        cases = [
            ([1, 1, 1], [-1, 2, 3], [[1, 1, 1]], [5]),
            ([1, 2, 3], [0, 0, 0], [[1, 1, 1]], [7]),
            ([1, 1, 1, 1], [3, -1, -2, 4], [[1, 1, 1, 1]], [6]),
            ([2, 1], [5, 5], [[1, 1]], [3]),
            ([1, 1, 1], [1, 1, 1], [[1, 1, 0], [0, 1, 1]], [2, 2]),
        ]
        for w, y, A, b in cases:
            with self.subTest(y=y):
                r_fast = solve_qp(w, y, A, b)
                r_ref = solve_qp_enumeration(w, y, A, b)
                self.assertEqual(r_fast.status, "ok")
                self.assertEqual(r_fast.x, r_ref.x)
                self.assertEqual(r_fast.objective, r_ref.objective)

    def test_matches_solver_on_pseudorandom_cases(self):
        import random
        rng = random.Random(20240601)
        for trial in range(30):
            n = rng.randint(2, 6)
            m = rng.randint(1, 2)
            w = [Fraction(rng.randint(1, 4), rng.randint(1, 3))
                 for _ in range(n)]
            y = [Fraction(rng.randint(-6, 6)) for _ in range(n)]
            # Build consistent overlapping sum constraints from a random
            # feasible point so the system is guaranteed feasible.
            x_feas = [Fraction(rng.randint(0, 5)) for _ in range(n)]
            A, b = [], []
            for _ in range(m):
                row = [Fraction(rng.randint(0, 1)) for _ in range(n)]
                if all(v == 0 for v in row):
                    row[rng.randrange(n)] = Fraction(1)
                A.append(row)
                b.append(sum(row[i] * x_feas[i] for i in range(n)))
            r_fast = solve_qp(w, y, A, b)
            r_ref = solve_qp_enumeration(w, y, A, b)
            self.assertEqual(r_fast.status, "ok", f"trial {trial}")
            self.assertEqual(r_fast.x, r_ref.x, f"trial {trial}")


if __name__ == "__main__":
    unittest.main()
