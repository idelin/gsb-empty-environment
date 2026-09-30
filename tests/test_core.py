"""Tests for the stable scalar evaluation of the amplification formula."""

import math
import unittest

from subsample_amp import (
    ApplicabilityError,
    SamplingModel,
    log_expm1,
    subsampled_epsilon,
)


class TestExactBoundaries(unittest.TestCase):
    def test_q_zero_gives_exact_zero(self):
        for eps in (0.0, 0.1, 1.0, 37.5, 700.0, 1000.0, 1e300):
            self.assertEqual(subsampled_epsilon(eps, 0.0), 0.0)

    def test_q_one_gives_eps_exactly(self):
        for eps in (0.1, 1.0, 5.5, 700.0, 1000.0, 1e300):
            self.assertEqual(subsampled_epsilon(eps, 1.0), eps)

    def test_eps_zero_gives_exact_zero(self):
        for q in (0.0, 1e-9, 0.3, 1.0):
            self.assertEqual(subsampled_epsilon(0.0, q), 0.0)


class TestStableEvaluation(unittest.TestCase):
    def test_matches_naive_formula_for_moderate_eps(self):
        for eps in (1e-6, 0.01, 0.5, 1.0, 3.0, 10.0):
            for q in (1e-4, 0.01, 0.5, 0.99):
                got = subsampled_epsilon(eps, q)
                want = math.log(1.0 + q * (math.exp(eps) - 1.0))
                self.assertAlmostEqual(got, want, places=14)

    def test_tiny_q_matches_first_order(self):
        eps, q = 2.0, 1e-9
        got = subsampled_epsilon(eps, q)
        first_order = q * math.expm1(eps)
        self.assertLess(abs(got - first_order) / first_order, 1e-7)

    def test_huge_eps_no_overflow(self):
        # exp(1000) overflows a float; the stable form must not.
        got = subsampled_epsilon(1000.0, 0.01)
        self.assertTrue(math.isfinite(got))
        self.assertAlmostEqual(got, 1000.0 + math.log(0.01), places=10)

    def test_huge_eps_tiny_q(self):
        got = subsampled_epsilon(1000.0, 1e-300)
        self.assertAlmostEqual(got, 1000.0 + math.log(1e-300), places=6)

    def test_astronomical_eps(self):
        got = subsampled_epsilon(1e300, 0.25)
        self.assertTrue(math.isfinite(got))
        self.assertAlmostEqual(got, 1e300 + math.log(0.25), delta=1e284)

    def test_result_never_exceeds_eps(self):
        for eps in (0.1, 1.0, 10.0, 700.0, 1000.0):
            for q in (1e-6, 0.5, 0.999):
                self.assertLessEqual(subsampled_epsilon(eps, q), eps)

    def test_monotone_in_q_and_eps(self):
        eps = 2.0
        qs = [0.0, 1e-6, 1e-3, 0.1, 0.5, 0.9, 1.0]
        vals = [subsampled_epsilon(eps, q) for q in qs]
        self.assertEqual(vals, sorted(vals))
        q = 0.3
        epss = [0.0, 0.01, 0.5, 2.0, 20.0, 800.0]
        vals = [subsampled_epsilon(e, q) for e in epss]
        self.assertEqual(vals, sorted(vals))


class TestLogExpm1(unittest.TestCase):
    def test_small_x(self):
        for x in (1e-8, 0.1, 1.0, 39.0):
            self.assertAlmostEqual(log_expm1(x), math.log(math.expm1(x)), places=12)

    def test_large_x_no_overflow(self):
        self.assertAlmostEqual(log_expm1(1000.0), 1000.0, places=10)
        self.assertAlmostEqual(log_expm1(50.0), 50.0 + math.log1p(-math.exp(-50.0)), places=12)

    def test_rejects_nonpositive(self):
        with self.assertRaises(ValueError):
            log_expm1(0.0)
        with self.assertRaises(ValueError):
            log_expm1(-1.0)


class TestRejection(unittest.TestCase):
    def test_invalid_q(self):
        for q in (-0.1, 1.0000001, 2.0, float("nan"), float("inf")):
            with self.assertRaises(ApplicabilityError):
                subsampled_epsilon(1.0, q)

    def test_invalid_eps(self):
        for eps in (-1e-9, -1.0, float("nan"), float("inf")):
            with self.assertRaises(ApplicabilityError):
                subsampled_epsilon(eps, 0.5)

    def test_non_poisson_model_rejected(self):
        for model in ("uniform_without_replacement", "shuffle", "fixed_size"):
            with self.assertRaises(ApplicabilityError):
                subsampled_epsilon(1.0, 0.5, model=model)

    def test_poisson_model_accepted_as_enum_and_string(self):
        a = subsampled_epsilon(1.0, 0.5, model=SamplingModel.POISSON)
        b = subsampled_epsilon(1.0, 0.5, model="poisson")
        self.assertEqual(a, b)

    def test_delta_positive_rejected(self):
        with self.assertRaises(ApplicabilityError):
            subsampled_epsilon(1.0, 0.5, base_delta=1e-5)

    def test_bool_and_wrong_type_rejected(self):
        with self.assertRaises(ApplicabilityError):
            subsampled_epsilon(True, 0.5)
        with self.assertRaises(ApplicabilityError):
            subsampled_epsilon(1.0, "0.5")


if __name__ == "__main__":
    unittest.main()
