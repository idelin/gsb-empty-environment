import math
import unittest

from bounded_estimation.confidence import (
    constant_observation_radius,
    hoeffding_radius,
    stage_delta,
    stage_sample_sizes,
    trivial_radius,
)


class TestHoeffdingRadius(unittest.TestCase):
    def test_matches_formula(self):
        n, delta = 100, 0.05
        expected = math.sqrt(math.log(2.0 / delta) / (2.0 * n))
        self.assertAlmostEqual(hoeffding_radius(n, 0.0, 1.0, delta), expected)

    def test_scales_with_width(self):
        r1 = hoeffding_radius(50, 0.0, 1.0, 0.1)
        r2 = hoeffding_radius(50, -2.0, 2.0, 0.1)
        self.assertAlmostEqual(r2, 4.0 * r1)

    def test_decreasing_in_n_increasing_in_confidence(self):
        radii = [hoeffding_radius(n, 0.0, 1.0, 0.05) for n in (10, 100, 1000)]
        self.assertTrue(radii[0] > radii[1] > radii[2])
        self.assertLess(
            hoeffding_radius(100, 0.0, 1.0, 0.5),
            hoeffding_radius(100, 0.0, 1.0, 0.01),
        )


class TestConstantObservationRadius(unittest.TestCase):
    def test_much_smaller_than_hoeffding_for_moderate_n(self):
        n, delta = 500, 0.05
        self.assertLess(
            constant_observation_radius(n, 0.0, 1.0, delta),
            0.2 * hoeffding_radius(n, 0.0, 1.0, delta),
        )

    def test_shrinks_like_one_over_n(self):
        delta = 0.05
        r1 = constant_observation_radius(1000, 0.0, 1.0, delta)
        r2 = constant_observation_radius(2000, 0.0, 1.0, delta)
        self.assertAlmostEqual(r1 / r2, 2.0, delta=0.05)


class TestStageSchedule(unittest.TestCase):
    def test_stage_deltas_sum_to_delta(self):
        delta = 0.07
        total = math.fsum(stage_delta(delta, k) for k in range(60))
        self.assertAlmostEqual(total, delta, places=12)

    def test_sizes_strictly_increasing(self):
        sizes = []
        gen = stage_sample_sizes(8, 2.0)
        for _ in range(6):
            sizes.append(next(gen))
        self.assertEqual(sizes, [8, 16, 32, 64, 128, 256])

    def test_slow_growth_still_increases(self):
        gen = stage_sample_sizes(3, 1.1)
        sizes = [next(gen) for _ in range(10)]
        self.assertTrue(all(b > a for a, b in zip(sizes, sizes[1:])))


class TestTrivialRadius(unittest.TestCase):
    def test_half_width(self):
        self.assertAlmostEqual(trivial_radius(2.0, 5.0), 1.5)


if __name__ == "__main__":
    unittest.main()
