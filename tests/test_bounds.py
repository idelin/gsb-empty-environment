import math
import unittest

from bounded_mean.bounds import (
    ZETA_2,
    bernoulli_endpoint_radius,
    cs_radius,
    hoeffding_radius,
    stage_delta,
    validate_delta,
    validate_epsilon,
)


class TestValidation(unittest.TestCase):
    def test_delta_range(self):
        for bad in (0.0, 1.0, -0.1, 1.5, float("nan"), float("inf")):
            with self.assertRaises(ValueError):
                validate_delta(bad)
        for bad in ("0.1", None, True):
            with self.assertRaises(TypeError):
                validate_delta(bad)
        self.assertEqual(validate_delta(0.05), 0.05)

    def test_epsilon_range(self):
        for bad in (0.0, -1.0, float("nan")):
            with self.assertRaises(ValueError):
                validate_epsilon(bad)


class TestHoeffding(unittest.TestCase):
    def test_known_value(self):
        r = hoeffding_radius(100, 1.0, 0.05)
        self.assertAlmostEqual(r, math.sqrt(math.log(40.0) / 200.0))

    def test_decreasing_in_n(self):
        radii = [hoeffding_radius(n, 1.0, 0.05) for n in (10, 100, 1000)]
        self.assertTrue(radii[0] > radii[1] > radii[2])

    def test_zero_width(self):
        self.assertEqual(hoeffding_radius(10, 0.0, 0.05), 0.0)


class TestConfidenceSequence(unittest.TestCase):
    def test_more_conservative_than_fixed(self):
        # Time-uniform validity must cost something at every n.
        for n in (1, 2, 3, 7, 64, 1000):
            self.assertGreaterEqual(cs_radius(n, 1.0, 0.05),
                                    hoeffding_radius(n, 1.0, 0.05))

    def test_epoch_allocation_sums_to_delta(self):
        delta = 0.05
        total = sum(delta / (ZETA_2 * (k + 1) ** 2) for k in range(100000))
        self.assertLess(total, delta)  # never overspends
        self.assertAlmostEqual(total, delta, places=4)

    def test_empirical_time_uniform_coverage(self):
        # EMPIRICAL check (labelled): the stitched sequence should easily
        # hold simultaneously over n = 1..256 for Bernoulli(0.5).
        import random

        rng = random.Random(20261001)
        trials, horizon, delta = 400, 256, 0.05
        violations = 0
        for _ in range(trials):
            total = 0.0
            for n in range(1, horizon + 1):
                total += 1.0 if rng.random() < 0.5 else 0.0
                if abs(total / n - 0.5) > cs_radius(n, 1.0, delta):
                    violations += 1
                    break
        # Bound is conservative; allow slack but expect far fewer than delta.
        self.assertLessEqual(violations, int(0.02 * trials))


class TestEndpointRule(unittest.TestCase):
    def test_shrinks_like_1_over_n(self):
        r1 = bernoulli_endpoint_radius(100, 1.0, 0.05)
        r2 = bernoulli_endpoint_radius(200, 1.0, 0.05)
        self.assertAlmostEqual(r1 / r2, 2.0, delta=0.1)

    def test_much_faster_than_hoeffding(self):
        n, delta = 1000, 0.05
        self.assertLess(bernoulli_endpoint_radius(n, 1.0, delta),
                        0.2 * hoeffding_radius(n, 1.0, delta))


class TestStageAllocation(unittest.TestCase):
    def test_stage_budgets_sum_to_delta(self):
        delta = 0.1
        total = sum(stage_delta(delta, j) for j in range(1, 100000))
        self.assertLess(total, delta)  # never overspends
        self.assertAlmostEqual(total, delta, places=4)


if __name__ == "__main__":
    unittest.main()
