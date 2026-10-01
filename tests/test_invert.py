import math
import unittest

from amplification import invert, stable


class TestAnalyticInverse(unittest.TestCase):
    def test_matches_direct_ratio_for_moderate_values(self):
        eps, budget = 2.0, 0.5
        direct = math.expm1(budget) / math.expm1(eps)
        self.assertAlmostEqual(invert._analytic_q(eps, budget), direct, places=15)

    def test_huge_eps_no_overflow(self):
        # expm1(1000) overflows float64; the log-space ratio must not.
        q = invert._analytic_q(1000.0, 690.0)
        self.assertTrue(math.isfinite(q))
        self.assertTrue(0.0 < q < 1.0)
        expected = math.exp(stable.log_expm1(690.0) - stable.log_expm1(1000.0))
        self.assertTrue(math.isclose(q, expected, rel_tol=1e-12))
        # A budget whose true q* underflows float64 yields 0.0, not an error.
        self.assertEqual(invert._analytic_q(1000.0, 5.0), 0.0)

    def test_clipping(self):
        self.assertEqual(invert._analytic_q(1.0, 0.0), 0.0)
        self.assertEqual(invert._analytic_q(1.0, 1.0), 1.0)
        self.assertEqual(invert._analytic_q(1.0, 5.0), 1.0)


class TestCertifiedBracket(unittest.TestCase):
    def check_bracket(self, eps, budget):
        res = invert.max_q_for_budget(eps, budget)
        # Analytic value enclosed by the certified bracket (up to the
        # relative precision loss of subnormal floats in extreme regimes;
        # rigorous enclosure of the true q* is checked against the
        # high-precision reference in test_reference.py).
        self.assertLessEqual(res.q_lo, res.q_hi)
        self.assertLessEqual(res.q_lo * (1.0 - 1e-9), res.q_analytic)
        self.assertLessEqual(res.q_analytic, res.q_hi * (1.0 + 1e-9))
        # Certificate semantics: q_lo certified feasible; q_hi certified
        # infeasible unless it is just the domain cap q <= 1.
        self.assertLessEqual(res.eps_at_q_lo.hi, budget)
        self.assertGreaterEqual(res.residual_lo, 0.0)
        if not res.q_hi_is_domain_cap:
            self.assertGreater(res.eps_at_q_hi.lo, budget)
            self.assertLess(res.residual_hi, 0.0)
        # Bracket is tight: a handful of ulps around the root.
        self.assertLessEqual(res.bracket_width, 1e-12 * max(1.0, res.q_hi))
        return res

    def test_roundtrip_recovers_q(self):
        for q in (1e-6, 1e-3, 0.1, 0.5, 0.999):
            eps = 3.0
            budget = stable.eps_prime(q, eps)
            res = self.check_bracket(eps, budget)
            self.assertAlmostEqual(res.q_analytic, q, delta=1e-12)

    def test_various_regimes(self):
        self.check_bracket(1.0, 0.1)
        self.check_bracket(10.0, 0.01)       # tiny resulting q
        self.check_bracket(700.0, 5.0)       # huge eps
        self.check_bracket(1000.0, 690.0)    # huge eps, large budget
        self.check_bracket(0.5, 0.5)         # budget == eps -> q = 1

    def test_budget_zero(self):
        res = invert.max_q_for_budget(2.0, 0.0)
        self.assertEqual(res.q_lo, 0.0)
        self.assertEqual(res.q_hi, 0.0)
        self.assertEqual(res.q_analytic, 0.0)

    def test_budget_at_least_eps_allows_full_sampling(self):
        res = invert.max_q_for_budget(2.0, 2.0)
        # q = 1 is the analytic answer; the certified lower bound is the
        # largest float whose outward-rounded interval still fits the budget.
        self.assertEqual(res.q_analytic, 1.0)
        self.assertEqual(res.q_hi, 1.0)
        self.assertGreater(res.q_lo, 1.0 - 1e-12)
        res = invert.max_q_for_budget(2.0, 5.0)
        self.assertEqual(res.q_lo, 1.0)
        self.assertEqual(res.q_hi, 1.0)
        self.assertTrue(res.q_hi_is_domain_cap)

    def test_rejects_invalid_inputs(self):
        with self.assertRaises(ValueError):
            invert.max_q_for_budget(-1.0, 1.0)
        with self.assertRaises(ValueError):
            invert.max_q_for_budget(1.0, -0.5)
        with self.assertRaises(ValueError):
            invert.max_q_for_budget(1.0, float("nan"))


if __name__ == "__main__":
    unittest.main()
