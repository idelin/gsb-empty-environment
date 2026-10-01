"""两阶段分层成组抽样的单元测试。"""

import math
import unittest

from stratified_sampling import (
    ClusterSample,
    MissingStratumError,
    SamplingDesignError,
    TwoStageStratum,
    ZeroInclusionProbabilityError,
    estimate_two_stage_mean,
)


def _hand_example():
    # 层 H:M=3 群、共 6 单元;抽 m=2 群,群内全查(pi_elem=1),pi_i=2/3。
    # z_A = 4/(2/3) = 6, z_B = 12/(2/3) = 18,t_hat = 24,mean = 24/6 = 4。
    stratum = TwoStageStratum(
        name="H",
        pop_clusters=3,
        pop_elements=6,
        clusters=(
            ClusterSample("A", 2, 2 / 3, 1.0, (1.0, 3.0)),
            ClusterSample("B", 2, 2 / 3, 1.0, (5.0, 7.0)),
        ),
    )
    return estimate_two_stage_mean([stratum])


class TestTwoStage(unittest.TestCase):
    def test_hand_computed_mean_and_variance(self):
        est = _hand_example()
        self.assertAlmostEqual(est.mean, 4.0)
        # s_z^2 = 72, v(total) = m(1-f)s_z^2 = 2*(1/3)*72 = 48,
        # v(mean) = 48/6^2 = 4/3
        self.assertTrue(est.variance_estimable)
        self.assertAlmostEqual(est.variance, 4.0 / 3.0)
        self.assertAlmostEqual(est.standard_error, math.sqrt(4.0 / 3.0))

    def test_inclusion_probabilities_applied(self):
        # 若忽略纳入概率,总量会是 4+12=16 而非 24,均值 8/3 而非 4。
        est = _hand_example()
        self.assertNotAlmostEqual(est.mean, 16.0 / 6.0)

    def test_zero_inclusion_probability_rejected(self):
        for bad in (0.0, -0.5, 1.5):
            with self.assertRaises(ZeroInclusionProbabilityError):
                ClusterSample("A", 2, bad, 1.0, (1.0,))
            with self.assertRaises(ZeroInclusionProbabilityError):
                ClusterSample("A", 2, 0.5, bad, (1.0,))

    def test_missing_stratum_rejected(self):
        with self.assertRaises(MissingStratumError):
            TwoStageStratum("H", pop_clusters=3, pop_elements=6, clusters=())

    def test_single_cluster_stratum_variance_not_estimable(self):
        stratum = TwoStageStratum(
            name="H",
            pop_clusters=5,
            pop_elements=10,
            clusters=(ClusterSample("A", 2, 0.2, 1.0, (1.0, 3.0)),),
        )
        est = estimate_two_stage_mean([stratum])
        self.assertFalse(est.variance_estimable)
        self.assertIsNone(est.variance)
        self.assertEqual(est.diagnostics[0].stratum, "H")

    def test_census_of_clusters_contributes_zero_variance(self):
        # 层内群全查(m=M):方差贡献为 0。
        stratum = TwoStageStratum(
            name="H",
            pop_clusters=2,
            pop_elements=4,
            clusters=(
                ClusterSample("A", 2, 1.0, 1.0, (1.0, 9.0)),
                ClusterSample("B", 2, 1.0, 1.0, (2.0, 8.0)),
            ),
        )
        est = estimate_two_stage_mean([stratum])
        self.assertTrue(est.variance_estimable)
        self.assertAlmostEqual(est.mean, 5.0)
        self.assertAlmostEqual(est.variance, 0.0)

    def test_variance_uses_clusters_not_elements(self):
        # 组内相关观测不当独立样本:方差只由"群"的聚合量决定。
        # 两个群,各 10 个完全相关的单元(值相同),与各 1 个单元的
        # 设计在最终群公式下给出同样的群间变异结构。
        many = TwoStageStratum(
            name="H",
            pop_clusters=4,
            pop_elements=40,
            clusters=(
                ClusterSample("A", 10, 0.5, 1.0, (1.0,) * 10),
                ClusterSample("B", 10, 0.5, 1.0, (3.0,) * 10),
            ),
        )
        est = estimate_two_stage_mean([many])
        # z_A = 20, z_B = 60, s_z^2 = 800, v(total) = m(1-f)s_z^2 = 2*0.5*800
        # = 800, v(mean) = 800/40^2 = 0.5。若误把 20 个单元当独立样本,
        # 会得到小得多的方差;这里必须等于 0.5。
        self.assertAlmostEqual(est.mean, 2.0)
        self.assertAlmostEqual(est.variance, 0.5)

    def test_empty_strata_rejected(self):
        with self.assertRaises(SamplingDesignError):
            estimate_two_stage_mean([])


if __name__ == "__main__":
    unittest.main()
