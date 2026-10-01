import math
import unittest

from amplification import intervals, stable
from amplification.intervals import Interval


class TestOutwardRounding(unittest.TestCase):
    def test_add_widens(self):
        a, b = Interval(0.1, 0.2), Interval(0.3, 0.4)
        r = intervals.add(a, b)
        self.assertLessEqual(r.lo, 0.1 + 0.3)
        self.assertGreaterEqual(r.hi, 0.2 + 0.4)

    def test_mul_nonneg(self):
        r = intervals.mul_nonneg(Interval(0.1, 0.2), Interval(2.0, 3.0))
        self.assertLessEqual(r.lo, 0.2)
        self.assertGreaterEqual(r.hi, 0.6)
        with self.assertRaises(ValueError):
            intervals.mul_nonneg(Interval(-1.0, 1.0), Interval(1.0, 2.0))

    def test_monotone_kernels_contain_true_value(self):
        x = 0.7
        iv = Interval(math.nextafter(x, -math.inf), math.nextafter(x, math.inf))
        self.assertTrue(intervals.exp(iv).contains(math.exp(x)))
        self.assertTrue(intervals.log(Interval(1.0, 2.0)).contains(math.log(1.5)))
        self.assertTrue(intervals.log1p(iv).contains(math.log1p(x)))

    def test_log_of_zero_is_minus_inf(self):
        r = intervals.log(Interval(0.0, 1.0))
        self.assertEqual(r.lo, -math.inf)
        self.assertGreaterEqual(r.hi, 0.0)  # widened upward from log(1) = 0


class TestEpsPrimeInterval(unittest.TestCase):
    def check_contains_point(self, q, eps):
        iv = intervals.eps_prime(intervals.const(q), intervals.const(eps))
        point = stable.eps_prime(q, eps)
        self.assertTrue(
            iv.contains(point),
            f"q={q}, eps={eps}: {point!r} not in {iv!r}",
        )
        return iv

    def test_contains_point_value_across_regimes(self):
        cases = [
            (0.0, 1.0), (1.0, 3.0), (0.5, 0.0),
            (1e-12, 1.0), (0.5, 1e-8),          # tiny q / tiny eps
            (1e-3, 700.0), (1e-3, 1000.0),      # huge eps, no overflow
            (1e-300, 800.0),                    # moderate result, huge factors
            (0.1, 1.0), (0.9, 5.0),
        ]
        for q, eps in cases:
            self.check_contains_point(q, eps)

    def test_boundaries_contain_exact_zero(self):
        iv = intervals.eps_prime(intervals.const(0.0), intervals.const(2.0))
        self.assertTrue(iv.contains(0.0))
        iv = intervals.eps_prime(intervals.const(0.5), intervals.const(0.0))
        self.assertTrue(iv.contains(0.0))

    def test_q_one_interval_contains_eps(self):
        iv = intervals.eps_prime(intervals.const(1.0), intervals.const(2.5))
        self.assertTrue(iv.contains(2.5))
        self.assertLess(iv.width, 1e-12)

    def test_straddling_branch_cut(self):
        # q chosen so log_t ~ log(1e-5): the interval straddles both branches.
        eps = 1.0
        q = 1e-5 / math.expm1(eps)
        q_iv = Interval(math.nextafter(q, 0.0), math.nextafter(q, 2.0))
        iv = intervals.eps_prime(q_iv, intervals.const(eps))
        self.assertTrue(iv.contains(stable.eps_prime(q, eps)))

    def test_interval_width_small_for_moderate_inputs(self):
        q_iv = Interval(0.09, 0.11)
        eps_iv = Interval(0.99, 1.01)
        iv = intervals.eps_prime(q_iv, eps_iv)
        self.assertLess(iv.width, 5e-2)
        self.assertTrue(iv.contains(stable.eps_prime(0.1, 1.0)))

    def test_rejects_out_of_domain_intervals(self):
        with self.assertRaises(ValueError):
            intervals.eps_prime(Interval(-0.1, 0.5), Interval(1.0, 1.0))
        with self.assertRaises(ValueError):
            intervals.eps_prime(Interval(0.1, 1.5), Interval(1.0, 1.0))
        with self.assertRaises(ValueError):
            intervals.eps_prime(Interval(0.1, 0.5), Interval(-1.0, 1.0))


if __name__ == "__main__":
    unittest.main()
