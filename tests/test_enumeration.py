"""全设计枚举(精确)与模拟(经验)核验;随机源注入与记录。"""

import random
import unittest
from fractions import Fraction
from statistics import fmean

from stratified_sampling import (
    IncrementalStratifiedEstimator,
    draw_stratified_sample,
    draw_two_stage_sample,
    estimate_stratified_mean,
    estimate_two_stage_mean,
    enumerate_stratified,
    enumerate_two_stage,
)


class TestExactEnumeration(unittest.TestCase):
    """精确结论:由有限总体全设计枚举得出,标记为"精确"。"""

    def test_stratified_unbiased_and_variance_exact(self):
        pop = {"A": [1.0, 2.0, 3.0, 4.0], "B": [2.0, 4.0, 6.0, 8.0, 10.0, 12.0]}
        res = enumerate_stratified(pop, {"A": 2, "B": 3})
        self.assertEqual(res.label, "精确(全设计枚举)")
        self.assertEqual(res.n_designs, 6 * 20)  # C(4,2)*C(6,3)
        self.assertTrue(res.unbiased)
        self.assertTrue(res.variance_matches_formula)
        self.assertEqual(res.expectation, res.true_mean)
        self.assertEqual(res.variance, res.formula_variance)

    def test_two_stage_unbiased_exact_census_second_stage(self):
        pop = {
            "H": {"c1": [1.0, 3.0], "c2": [5.0, 7.0], "c3": [2.0, 8.0]},
            "K": {"d1": [4.0, 6.0], "d2": [10.0, 12.0]},
        }
        res = enumerate_two_stage(pop, {"H": 2, "K": 1}, elements_per_cluster=2)
        self.assertEqual(res.label, "精确(全设计枚举)")
        self.assertTrue(res.unbiased)
        # 二阶段全查:最终群方差公式精确成立
        self.assertTrue(res.variance_matches_formula)

    def test_two_stage_unbiased_exact_subsampled_second_stage(self):
        pop = {
            "H": {"c1": [1.0, 3.0], "c2": [5.0, 7.0], "c3": [2.0, 8.0]},
        }
        res = enumerate_two_stage(pop, {"H": 2}, elements_per_cluster=1)
        self.assertTrue(res.unbiased)
        # 二阶段非全查:最终群公式不再精确,不给精确结论
        self.assertIsNone(res.formula_variance)
        self.assertIsNone(res.variance_matches_formula)


class TestEmpiricalSimulation(unittest.TestCase):
    """经验结论:由注入随机源的模拟得出,标记为"经验",与精确结论分开。"""

    LABEL = "经验(模拟)"

    def test_empirical_unbiasedness_stratified(self):
        pop = {"A": [1.0, 2.0, 3.0, 4.0, 5.0], "B": [2.0, 4.0, 6.0, 8.0, 10.0]}
        sizes = {"A": 2, "B": 3}
        true_mean = fmean([y for vs in pop.values() for y in vs])
        rng = random.Random(20261001)
        reps = 4000
        estimates = []
        for _ in range(reps):
            samples, _ = draw_stratified_sample(pop, sizes, rng)
            estimates.append(estimate_stratified_mean(samples).mean)
        empirical_mean = fmean(estimates)
        # 经验结论:容差内接近真值(固定 seed,结果确定)
        self.assertAlmostEqual(empirical_mean, true_mean, delta=0.05)

    def test_empirical_variance_matches_formula(self):
        pop = {"A": [1.0, 2.0, 3.0, 4.0, 5.0], "B": [2.0, 4.0, 6.0, 8.0, 10.0]}
        sizes = {"A": 2, "B": 3}
        rng = random.Random(777)
        reps = 4000
        estimates = []
        variances = []
        for _ in range(reps):
            samples, _ = draw_stratified_sample(pop, sizes, rng)
            est = estimate_stratified_mean(samples)
            estimates.append(est.mean)
            variances.append(est.variance)
        mu = fmean(estimates)
        empirical_var = fmean([(e - mu) ** 2 for e in estimates])
        mean_var_estimate = fmean(variances)
        # 经验结论:方差估计的均值应接近估计量的真实(模拟)方差
        self.assertAlmostEqual(mean_var_estimate, empirical_var, delta=0.05)


class TestRandomSourceInjection(unittest.TestCase):
    def test_seed_and_sizes_recorded(self):
        pop = {"A": [1.0, 2.0, 3.0], "B": [4.0, 5.0, 6.0, 7.0]}
        rng = random.Random(42)
        samples, record = draw_stratified_sample(pop, {"A": 2, "B": 2}, rng, seed=42)
        self.assertEqual(record.seed, 42)
        self.assertEqual(record.sample_sizes, {"A": 2, "B": 2})
        self.assertEqual(record.method, "stratified-srs-wor")

    def test_reproducible_with_same_seed(self):
        pop = {"A": [1.0, 2.0, 3.0, 4.0], "B": [5.0, 6.0, 7.0, 8.0]}
        s1, _ = draw_stratified_sample(pop, {"A": 2, "B": 2}, random.Random(9), seed=9)
        s2, _ = draw_stratified_sample(pop, {"A": 2, "B": 2}, random.Random(9), seed=9)
        self.assertEqual([s.values for s in s1], [s.values for s in s2])

    def test_two_stage_record_detail(self):
        pop = {"H": {"c1": [1.0, 2.0], "c2": [3.0, 4.0], "c3": [5.0, 6.0]}}
        rng = random.Random(5)
        strata, record = draw_two_stage_sample(pop, {"H": 2}, 1, rng, seed=5)
        self.assertEqual(record.seed, 5)
        self.assertEqual(record.sample_sizes, {"H": 2})
        self.assertEqual(sum(record.detail["H"].values()), 2)
        est = estimate_two_stage_mean(strata)
        self.assertIsNotNone(est.mean)


if __name__ == "__main__":
    unittest.main()
