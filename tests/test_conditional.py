import math
import random
import unittest

from psd_gaussian import (
    InfeasibleConditionError,
    condition_gaussian,
    sample_correlated,
)


class TestConditionalGaussian(unittest.TestCase):
    def test_conditioning_two_independent_normals(self):
        # x, y iid N(0,1); condition on x + y = 1.
        # Exact theory: mean (1/2, 1/2), covariance [[1/2, -1/2], [-1/2, 1/2]].
        cond = condition_gaussian(
            [0.0, 0.0], [[1.0, 0.0], [0.0, 1.0]], [[1.0, 1.0]], [1.0]
        )
        self.assertAlmostEqual(cond.mean[0], 0.5, places=10)
        self.assertAlmostEqual(cond.mean[1], 0.5, places=10)
        expected = [[0.5, -0.5], [-0.5, 0.5]]
        for i in range(2):
            for j in range(2):
                self.assertAlmostEqual(cond.covariance[i][j], expected[i][j], places=10)

    def test_zero_variance_direction_reported(self):
        cond = condition_gaussian(
            [0.0, 0.0], [[1.0, 0.0], [0.0, 1.0]], [[1.0, 1.0]], [1.0]
        )
        self.assertEqual(len(cond.zero_variance_directions), 1)
        direction = cond.zero_variance_directions[0]
        # The pinned direction is (1, 1) / sqrt(2) (up to sign).
        self.assertAlmostEqual(abs(direction[0]), 1.0 / math.sqrt(2.0), places=8)
        self.assertAlmostEqual(abs(direction[1]), 1.0 / math.sqrt(2.0), places=8)

    def test_conditional_samples_satisfy_constraint(self):
        cond = condition_gaussian(
            [0.0, 0.0], [[1.0, 0.0], [0.0, 1.0]], [[1.0, 1.0]], [1.0]
        )
        samples, _ = sample_correlated(
            cond.decomposition, cond.mean, 500, random.Random(11), seed=11
        )
        for s in samples:
            self.assertAlmostEqual(s[0] + s[1], 1.0, places=8)

    def test_condition_on_deterministic_coordinate(self):
        # Sigma = diag(1, 0): x2 is deterministically mu2.
        cond = condition_gaussian(
            [0.0, 3.0], [[1.0, 0.0], [0.0, 0.0]], [[0.0, 1.0]], [3.0]
        )
        self.assertAlmostEqual(cond.mean[1], 3.0, places=12)
        self.assertTrue(any(abs(d[1]) > 0.99 for d in cond.zero_variance_directions))

    def test_infeasible_outside_support(self):
        # x2 has zero variance and equals 3; conditioning on x2 = 4 has
        # probability zero and must be rejected, not smoothed over.
        with self.assertRaises(InfeasibleConditionError) as ctx:
            condition_gaussian(
                [0.0, 3.0], [[1.0, 0.0], [0.0, 0.0]], [[0.0, 1.0]], [4.0]
            )
        self.assertEqual(ctx.exception.reason, "outside_support")

    def test_inconsistent_constraints(self):
        with self.assertRaises(InfeasibleConditionError) as ctx:
            condition_gaussian(
                [0.0, 0.0],
                [[1.0, 0.0], [0.0, 1.0]],
                [[1.0, 1.0], [1.0, 1.0]],
                [1.0, 2.0],
            )
        self.assertEqual(ctx.exception.reason, "inconsistent_constraints")

    def test_conditional_sampling_is_deterministic(self):
        cond = condition_gaussian(
            [1.0, -1.0], [[2.0, 0.3], [0.3, 1.0]], [[1.0, 2.0]], [0.5]
        )
        s1, _ = sample_correlated(
            cond.decomposition, cond.mean, 30, random.Random(21), seed=21
        )
        s2, _ = sample_correlated(
            cond.decomposition, cond.mean, 30, random.Random(21), seed=21
        )
        self.assertEqual(s1, s2)


if __name__ == "__main__":
    unittest.main()
