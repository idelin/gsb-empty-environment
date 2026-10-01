"""增量统计:样本身份变更时增量撤回贡献的单元测试。"""

import unittest

from stratified_sampling import (
    IncrementalStratifiedEstimator,
    SamplingDesignError,
    StratumSample,
    estimate_stratified_mean,
)


def _batch(pop_sizes, data):
    return estimate_stratified_mean([
        StratumSample(name, pop_sizes[name], tuple(values))
        for name, values in data.items()
    ])


class TestIncremental(unittest.TestCase):
    def setUp(self):
        self.pop_sizes = {"A": 10, "B": 20}

    def test_add_matches_batch(self):
        data = {"A": [1.0, 2.0, 4.0], "B": [3.0, 5.0, 7.0, 9.0]}
        inc = IncrementalStratifiedEstimator(self.pop_sizes)
        for name, values in data.items():
            for v in values:
                inc.add(name, v)
        got = inc.estimate()
        want = _batch(self.pop_sizes, data)
        self.assertAlmostEqual(got.mean, want.mean)
        self.assertAlmostEqual(got.variance, want.variance)
        self.assertEqual(got.sample_sizes, {"A": 3, "B": 4})

    def test_retract_on_identity_change(self):
        # 样本身份变更:撤回旧成员贡献,追加新成员,结果与批量重算一致。
        inc = IncrementalStratifiedEstimator(self.pop_sizes)
        for v in (1.0, 2.0, 4.0):
            inc.add("A", v)
        for v in (3.0, 5.0, 7.0, 9.0):
            inc.add("B", v)
        inc.retract("A", 2.0)   # 成员 2.0 出样
        inc.add("A", 10.0)      # 成员 10.0 入样
        inc.retract("B", 9.0)
        got = inc.estimate()
        want = _batch(self.pop_sizes, {"A": [1.0, 4.0, 10.0], "B": [3.0, 5.0, 7.0]})
        self.assertAlmostEqual(got.mean, want.mean)
        self.assertAlmostEqual(got.variance, want.variance)

    def test_retract_from_empty_rejected(self):
        inc = IncrementalStratifiedEstimator(self.pop_sizes)
        with self.assertRaises(SamplingDesignError):
            inc.retract("A", 1.0)

    def test_add_beyond_population_rejected(self):
        inc = IncrementalStratifiedEstimator({"A": 2})
        inc.add("A", 1.0)
        inc.add("A", 2.0)
        with self.assertRaises(SamplingDesignError):
            inc.add("A", 3.0)

    def test_variance_diagnostic_surfaces_incrementally(self):
        # 撤回到单样本层时,方差不可估的诊断同样出现。
        inc = IncrementalStratifiedEstimator(self.pop_sizes)
        inc.add("A", 1.0)
        inc.add("A", 3.0)
        inc.add("B", 2.0)
        inc.add("B", 4.0)
        self.assertTrue(inc.estimate().variance_estimable)
        inc.retract("A", 1.0)  # A 层只剩 1 个样本
        est = inc.estimate()
        self.assertFalse(est.variance_estimable)
        self.assertIsNone(est.variance)


if __name__ == "__main__":
    unittest.main()
