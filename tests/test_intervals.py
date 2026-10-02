import random
import unittest

from bootci.errors import AccelerationNotEstimableError, BCANotEstimableError
from bootci.intervals import Interval, bca_interval, percentile_interval
from bootci.jackknife import acceleration


class TestPercentileInterval(unittest.TestCase):
    def test_known_values_type7(self):
        stats = [float(i) for i in range(1, 101)]
        iv = percentile_interval(stats, 0.1, "type7")
        # type7: Q(p) = 1 + p*(n-1)
        self.assertAlmostEqual(iv.low, 1 + 0.05 * 99)
        self.assertAlmostEqual(iv.high, 1 + 0.95 * 99)
        self.assertEqual(iv.method, "percentile")
        self.assertEqual(iv.quantile_method, "type7")

    def test_requires_two_stats(self):
        with self.assertRaises(ValueError):
            percentile_interval([1.0], 0.05)
        with self.assertRaises(ValueError):
            percentile_interval([1.0, 2.0], 0.0)


class TestAcceleration(unittest.TestCase):
    def test_known_value(self):
        # jack stats [1,2,4]: mean=7/3, diffs=[4/3,1/3,-5/3]
        ss = 16 / 9 + 1 / 9 + 25 / 9
        num = 64 / 27 + 1 / 27 - 125 / 27
        expect = num / (6.0 * ss ** 1.5)
        self.assertAlmostEqual(acceleration([1.0, 2.0, 4.0]), expect)

    def test_degenerate_raises(self):
        with self.assertRaises(AccelerationNotEstimableError):
            acceleration([3.0, 3.0, 3.0])
        with self.assertRaises(AccelerationNotEstimableError):
            acceleration([1.0])


class TestBCaInterval(unittest.TestCase):
    def test_basic_interval(self):
        rng = random.Random(0)
        stats = sorted(rng.gauss(0, 1) for _ in range(2000))
        jk = [rng.gauss(0, 0.1) for _ in range(50)]
        iv = bca_interval(stats, 0.05, jk, 0.05)
        self.assertIsInstance(iv, Interval)
        self.assertEqual(iv.method, "bca")
        self.assertLess(iv.low, iv.high)
        self.assertGreaterEqual(iv.low, stats[0])
        self.assertLessEqual(iv.high, stats[-1])

    def test_z0_infinite_raises(self):
        # 所有重采样统计量 >= 观测值 → z0 = -inf，显式失败
        stats = [1.0, 2.0, 3.0, 4.0]
        with self.assertRaises(BCANotEstimableError):
            bca_interval(stats, 0.5, [1.0, 2.0, 3.0], 0.05)
        # 所有重采样统计量 < 观测值 → z0 = +inf
        with self.assertRaises(BCANotEstimableError):
            bca_interval(stats, 5.0, [1.0, 2.0, 3.0], 0.05)

    def test_degenerate_jackknife_raises(self):
        stats = [0.0, 1.0, 2.0, 3.0]
        with self.assertRaises(AccelerationNotEstimableError):
            bca_interval(stats, 1.5, [2.0, 2.0, 2.0], 0.05)


if __name__ == "__main__":
    unittest.main()
