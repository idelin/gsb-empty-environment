"""分层均值/方差估计的单元测试。"""

import math
import unittest

from stratified_sampling import (
    MissingStratumError,
    SamplingDesignError,
    StratumSample,
    estimate_stratified_mean,
)


class TestStratifiedMean(unittest.TestCase):
    def test_weights_sum_to_one(self):
        est = estimate_stratified_mean([
            StratumSample("A", 4, (1.0, 3.0)),
            StratumSample("B", 6, (2.0, 4.0, 6.0)),
        ])
        self.assertAlmostEqual(sum(est.weights.values()), 1.0)
        self.assertAlmostEqual(est.weights["A"], 0.4)
        self.assertAlmostEqual(est.weights["B"], 0.6)

    def test_unequal_strata_not_simple_average(self):
        # 大小不同的层不能简单平均层均值:层均值为 2 与 4,
        # 简单平均是 3.0,加权(0.4/0.6)结果必须是 3.2。
        est = estimate_stratified_mean([
            StratumSample("A", 4, (1.0, 3.0)),
            StratumSample("B", 6, (2.0, 4.0, 6.0)),
        ])
        self.assertNotAlmostEqual(est.mean, 3.0)
        self.assertAlmostEqual(est.mean, 3.2)

    def test_hand_computed_variance(self):
        # A: W=0.4, n=2, s^2=2 -> 0.16*(1-0.5)*2/2 = 0.08
        # B: W=0.6, n=3, s^2=4 -> 0.36*(1-0.5)*4/3 = 0.24
        est = estimate_stratified_mean([
            StratumSample("A", 4, (1.0, 3.0)),
            StratumSample("B", 6, (2.0, 4.0, 6.0)),
        ])
        self.assertTrue(est.variance_estimable)
        self.assertAlmostEqual(est.variance, 0.32)
        self.assertAlmostEqual(est.standard_error, math.sqrt(0.32))

    def test_census_stratum_contributes_zero_variance(self):
        # A 层全查(n=N=2),即使层内有变异,方差贡献也必须为 0。
        est = estimate_stratified_mean([
            StratumSample("A", 2, (1.0, 3.0)),          # 全查
            StratumSample("B", 8, (2.0, 4.0, 6.0, 8.0)),  # n=4
        ])
        self.assertAlmostEqual(est.mean, 0.2 * 2.0 + 0.8 * 5.0)
        # 仅 B 层贡献:0.64*(1-0.5)*(20/3)/4 = 8/15
        self.assertAlmostEqual(est.variance, 8.0 / 15.0)

    def test_single_sample_stratum_variance_not_estimable(self):
        # 单样本层:均值照算,方差不可估,绝不伪造精确方差。
        est = estimate_stratified_mean([
            StratumSample("A", 100, (2.5,)),            # n=1 < N
            StratumSample("B", 100, (1.0, 3.0, 5.0)),
        ])
        self.assertAlmostEqual(est.mean, 0.5 * 2.5 + 0.5 * 3.0)
        self.assertFalse(est.variance_estimable)
        self.assertIsNone(est.variance)
        self.assertIsNone(est.standard_error)
        self.assertEqual(len(est.diagnostics), 1)
        self.assertEqual(est.diagnostics[0].stratum, "A")

    def test_single_sample_census_stratum_is_exact(self):
        # n=1 且 N=1:全查,该层无抽样误差,方差可估(贡献 0)。
        est = estimate_stratified_mean([
            StratumSample("A", 1, (7.0,)),
            StratumSample("B", 4, (1.0, 3.0)),
        ])
        self.assertTrue(est.variance_estimable)
        self.assertIsNotNone(est.variance)

    def test_missing_stratum_rejected(self):
        with self.assertRaises(MissingStratumError):
            StratumSample("A", 50, ())

    def test_sample_larger_than_population_rejected(self):
        with self.assertRaises(SamplingDesignError):
            StratumSample("A", 2, (1.0, 2.0, 3.0))

    def test_zero_population_rejected(self):
        with self.assertRaises(SamplingDesignError):
            StratumSample("A", 0, (1.0,))

    def test_empty_strata_rejected(self):
        with self.assertRaises(SamplingDesignError):
            estimate_stratified_mean([])


if __name__ == "__main__":
    unittest.main()
