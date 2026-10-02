import unittest

from bootci.core import exact_bootstrap
from bootci.errors import EnumerationTooLargeError
from bootci.exact import exact_distribution, exact_percentile_interval
from bootci.intervals import DegenerateInterval, Interval
from bootci.rng import StreamFactory
from bootci.resample import resample_one
from bootci.samples import OneSample, PairedSample, StratifiedSample, TwoSample


def mean(v):
    return sum(v) / len(v)


class TestExactOneSample(unittest.TestCase):
    def test_mean_distribution_n2(self):
        # {0,1}，均值分布：0(w1), 0.5(w2), 1(w1)，总权重 4
        dist, total = exact_distribution(OneSample([0.0, 1.0]), mean)
        self.assertEqual(total, 4)
        self.assertEqual(dist, {0.0: 1, 0.5: 2, 1.0: 1})

    def test_mean_distribution_n3(self):
        dist, total = exact_distribution(OneSample([1.0, 2.0, 3.0]), mean)
        self.assertEqual(total, 27)
        self.assertEqual(dist[1.0], 1)   # 全为 1
        self.assertEqual(dist[3.0], 1)   # 全为 3
        self.assertEqual(dist[2.0], 7)   # {2,2,2}(1) + {1,2,3}(6)
        self.assertEqual(sum(dist.values()), 27)

    def test_exact_interval(self):
        # 分布 {0:1, 0.5:2, 1:1}，alpha=0.5 → [Q(0.25), Q(0.75)] = [0, 0.5]
        low, high = exact_percentile_interval({0.0: 1, 0.5: 2, 1.0: 1}, 4, 0.5)
        self.assertEqual((low, high), (0.0, 0.5))


class TestExactPaired(unittest.TestCase):
    def test_pairs_enumerated_as_units(self):
        # 对 (0,5) 与 (1,3)：统计量 mean(y)-mean(x)
        sample = PairedSample([0.0, 1.0], [5.0, 3.0])
        dist, total = exact_distribution(sample, lambda x, y: mean(y) - mean(x))
        self.assertEqual(total, 4)
        self.assertEqual(dist, {5.0: 1, 3.5: 2, 2.0: 1})


class TestExactTwoSample(unittest.TestCase):
    def test_grouped_distribution(self):
        sample = TwoSample([0.0, 1.0], [2.0])
        dist, total = exact_distribution(sample, lambda x, y: mean(y) - mean(x))
        self.assertEqual(total, 4)
        self.assertEqual(dist, {2.0: 1, 1.5: 2, 1.0: 1})


class TestExactStratified(unittest.TestCase):
    def test_stratified_distribution(self):
        sample = StratifiedSample(((0.0, 1.0), (10.0,)))
        stat = lambda strata: mean(strata[0] + strata[1])
        dist, total = exact_distribution(sample, stat)
        self.assertEqual(total, 4)
        self.assertEqual(dist[4.0], 1)          # 层0均值1 → (2+10)/3
        self.assertEqual(dist[11 / 3], 2)       # 层0均值0.5
        self.assertEqual(dist[10 / 3], 1)       # 层0均值0


class TestExactVsEmpirical(unittest.TestCase):
    def test_empirical_converges_to_exact(self):
        # 参考验证：大 B 经验分布应逼近精确枚举分布
        sample = OneSample([1.0, 2.0, 3.0, 4.0])
        dist, total = exact_distribution(sample, mean)
        exact_p = sum(w for v, w in dist.items() if v <= 2.0) / total

        rng = StreamFactory(7).stream("exact-vs-empirical")
        n_rep = 40_000
        hits = sum(1 for _ in range(n_rep) if mean(resample_one(sample, rng)) <= 2.0)
        self.assertAlmostEqual(hits / n_rep, exact_p, delta=0.01)


class TestExactBootstrapReport(unittest.TestCase):
    def test_exact_nature_and_metadata(self):
        report = exact_bootstrap(OneSample([0.0, 1.0]), mean, alpha=0.5)
        self.assertEqual(report.nature, "exact")
        self.assertIsNone(report.master_seed)
        self.assertIsNone(report.n_resamples)
        self.assertEqual(report.sample_sizes, {"n": 2})
        self.assertEqual(report.support_size, 3)
        iv = report.intervals["percentile"]
        self.assertIsInstance(iv, Interval)
        self.assertEqual((iv.low, iv.high), (0.0, 0.5))
        self.assertEqual(iv.quantile_method, "weighted-type1")

    def test_exact_degenerate(self):
        report = exact_bootstrap(OneSample([4.0, 4.0, 4.0]), mean)
        self.assertTrue(report.degenerate)
        iv = report.intervals["percentile"]
        self.assertIsInstance(iv, DegenerateInterval)
        self.assertEqual(iv.point, 4.0)

    def test_enumeration_too_large(self):
        with self.assertRaises(EnumerationTooLargeError):
            exact_distribution(OneSample(list(range(30))), mean)


if __name__ == "__main__":
    unittest.main()
