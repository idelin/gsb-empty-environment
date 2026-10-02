import unittest
import unittest.mock as mock

import bootstrap_ci.intervals as intervals_mod
from bootstrap_ci.errors import BcaNotEstimableError, DegenerateJackknifeError
from bootstrap_ci.intervals import (
    bca_interval,
    jackknife_acceleration,
    percentile_interval,
)


class TestPercentileInterval(unittest.TestCase):
    def test_type7_endpoints(self):
        boot = sorted(float(i) for i in range(1000))
        lo, hi = percentile_interval(boot, 0.05)
        # type-7: h = 999 * 0.025 = 24.975 ; h = 999 * 0.975 = 974.025
        self.assertAlmostEqual(lo, 24.975)
        self.assertAlmostEqual(hi, 974.025)

    def test_degenerate_distribution_is_a_point(self):
        lo, hi = percentile_interval([3.5] * 100, 0.05)
        self.assertEqual((lo, hi), (3.5, 3.5))

    def test_alpha_validation(self):
        with self.assertRaises(ValueError):
            percentile_interval([1.0, 2.0], 0.0)
        with self.assertRaises(ValueError):
            percentile_interval([1.0, 2.0], 1.0)


class TestJackknifeAcceleration(unittest.TestCase):
    def test_symmetric_replicates_give_zero(self):
        self.assertEqual(jackknife_acceleration([1.0, 2.0, 3.0]), 0.0)

    def test_known_value(self):
        # jackknife values 0, 0, 1: mean = 1/3
        # diffs: 1/3, 1/3, -2/3 ; num = 2/27 - 8/27 = -6/27
        # den = 6 * (2/9 + 4/9)^1.5 = 6 * (6/9)^1.5
        vals = [0.0, 0.0, 1.0]
        expected = (-6.0 / 27.0) / (6.0 * (6.0 / 9.0) ** 1.5)
        self.assertAlmostEqual(jackknife_acceleration(vals), expected)

    def test_degenerate_jackknife_fails_explicitly(self):
        with self.assertRaises(DegenerateJackknifeError):
            jackknife_acceleration([2.0, 2.0, 2.0, 2.0])

    def test_too_few_replicates(self):
        with self.assertRaises(DegenerateJackknifeError):
            jackknife_acceleration([1.0])


class TestBcaInterval(unittest.TestCase):
    def test_reduces_to_percentile_when_z0_and_a_are_zero(self):
        boot = sorted(float(i) for i in range(1000))
        lo, hi = bca_interval(boot, observed=499.5,
                              jackknife_stats=[1.0, 2.0, 3.0], alpha=0.05)
        plo, phi = percentile_interval(boot, 0.05)
        self.assertAlmostEqual(lo, plo)
        self.assertAlmostEqual(hi, phi)

    def test_bias_correction_shifts_interval(self):
        boot = sorted(float(i) for i in range(1000))
        # 75% of replicates below the observed value -> z0 > 0 -> shift right
        lo, hi = bca_interval(boot, observed=749.5,
                              jackknife_stats=[1.0, 2.0, 3.0], alpha=0.05)
        plo, phi = percentile_interval(boot, 0.05)
        self.assertGreater(lo, plo)
        self.assertGreater(hi, phi)

    def test_z0_not_finite_fails_explicitly(self):
        boot = [1.0] * 500 + [2.0] * 500
        with self.assertRaises(BcaNotEstimableError):
            bca_interval(boot, observed=0.5,  # nothing below observed
                         jackknife_stats=[1.0, 2.0, 3.0], alpha=0.05)
        with self.assertRaises(BcaNotEstimableError):
            bca_interval(boot, observed=3.0,  # everything below observed
                         jackknife_stats=[1.0, 2.0, 3.0], alpha=0.05)

    def test_degenerate_jackknife_propagates(self):
        boot = sorted(float(i) for i in range(100))
        with self.assertRaises(DegenerateJackknifeError):
            bca_interval(boot, observed=50.0,
                         jackknife_stats=[1.0, 1.0, 1.0], alpha=0.05)

    def test_zero_adjustment_denominator_fails(self):
        class FakeNorm:
            def inv_cdf(self, p):
                return 0.0 if p == 0.5 else 1.0

            def cdf(self, x):
                return 0.5

        boot = [0.0] * 500 + [1.0] * 500  # prop below observed=0.5 is 0.5
        with mock.patch.object(intervals_mod, "_NORM", FakeNorm()), \
                mock.patch.object(intervals_mod, "jackknife_acceleration",
                                  return_value=1.0):
            # denom = 1 - a * (z0 + z) = 1 - 1.0 * (0.0 + 1.0) == 0 exactly
            with self.assertRaises(BcaNotEstimableError):
                bca_interval(boot, observed=0.5,
                             jackknife_stats=[0.0, 0.0, 1.0], alpha=0.05)


if __name__ == "__main__":
    unittest.main()
