import unittest

from bootci import (
    BCA,
    PERCENTILE,
    AccelerationNotEstimableError,
    BCANotEstimableError,
    DegenerateInterval,
    Interval,
    InvalidResampleError,
    OneSample,
    PairedSample,
    StratifiedSample,
    TimeSeriesSample,
    TwoSample,
    bootstrap,
)


def mean(v):
    return sum(v) / len(v)


def mean_diff(x, y):
    return mean(y) - mean(x)


class TestReproducibility(unittest.TestCase):
    def test_same_seed_same_result(self):
        sample = OneSample([1.0, 2.0, 3.0, 4.0, 5.0])
        r1 = bootstrap(sample, mean, n_resamples=500, seed=11)
        r2 = bootstrap(sample, mean, n_resamples=500, seed=11)
        self.assertEqual(r1.intervals, r2.intervals)

    def test_different_seed_differs(self):
        sample = OneSample([1.0, 2.0, 3.0, 4.0, 5.0])
        r1 = bootstrap(sample, mean, n_resamples=500, seed=11)
        r2 = bootstrap(sample, mean, n_resamples=500, seed=12)
        self.assertNotEqual(
            r1.intervals[PERCENTILE], r2.intervals[PERCENTILE]
        )

    def test_metadata_recorded(self):
        sample = OneSample([1.0, 2.0, 3.0], sample_id="demo")
        r = bootstrap(sample, mean, n_resamples=100, seed=5, methods=(PERCENTILE,))
        self.assertEqual(r.master_seed, 5)
        self.assertEqual(r.n_resamples, 100)
        self.assertEqual(r.sample_sizes, {"n": 3})
        self.assertEqual(r.nature, "empirical")
        self.assertEqual(r.sample_id, "demo")
        self.assertEqual(r.invalid_resamples, 0)


class TestParallelOrderIndependence(unittest.TestCase):
    DATA = {
        "alpha": (1.0, 2.5, 3.5, 4.0, 9.0),
        "beta": (0.5, 1.5, 2.0, 8.0),
    }

    def _run(self, order):
        reports = {}
        for sid in order:
            sample = OneSample(self.DATA[sid], sample_id=sid)
            reports[sid] = bootstrap(
                sample, mean, n_resamples=400, seed=99, methods=(PERCENTILE,)
            )
        return reports

    def test_processing_order_does_not_change_results(self):
        r1 = self._run(["alpha", "beta"])
        r2 = self._run(["beta", "alpha"])
        for sid in self.DATA:
            self.assertEqual(r1[sid].intervals, r2[sid].intervals)


class TestPairedBootstrap(unittest.TestCase):
    def test_pairs_resampled_together(self):
        # y = x + 1：若成对抽取，mean(y)-mean(x) 恒为 1 → 退化于 1.0
        x = tuple(float(i) for i in range(8))
        y = tuple(v + 1.0 for v in x)
        report = bootstrap(
            PairedSample(x, y), mean_diff,
            n_resamples=300, seed=3, methods=(PERCENTILE, BCA),
        )
        self.assertTrue(report.degenerate)
        for iv in report.intervals.values():
            self.assertIsInstance(iv, DegenerateInterval)
            self.assertEqual(iv.point, 1.0)

    def test_paired_length_mismatch_rejected(self):
        with self.assertRaises(ValueError):
            PairedSample([1.0, 2.0], [1.0])


class TestDegenerate(unittest.TestCase):
    def test_identical_values_point_interval(self):
        # 相同值样本：区间退化为一点，单独报告，BCa 不再尝试
        report = bootstrap(
            OneSample([7.0] * 5), mean,
            n_resamples=200, seed=1, methods=(PERCENTILE, BCA),
        )
        self.assertTrue(report.degenerate)
        self.assertEqual(report.estimate, 7.0)
        for iv in report.intervals.values():
            self.assertIsInstance(iv, DegenerateInterval)
            self.assertEqual(iv.point, 7.0)

    def test_nondegenerate_has_intervals(self):
        report = bootstrap(
            OneSample([1.0, 2.0, 3.0, 4.0, 5.0, 6.0]), mean,
            n_resamples=1000, seed=8, methods=(PERCENTILE, BCA),
        )
        self.assertFalse(report.degenerate)
        for method in (PERCENTILE, BCA):
            iv = report.intervals[method]
            self.assertIsInstance(iv, Interval)
            self.assertLess(iv.low, report.estimate)
            self.assertGreater(iv.high, report.estimate)


class TestInvalidResamples(unittest.TestCase):
    @staticmethod
    def fragile_stat(v):
        if len(set(v)) == 1:
            raise ZeroDivisionError("重采样退化")
        return mean(v)

    def test_default_raises(self):
        with self.assertRaises(InvalidResampleError):
            bootstrap(
                OneSample([1.0, 2.0, 3.0]), self.fragile_stat,
                n_resamples=200, seed=1, methods=(PERCENTILE,),
            )

    def test_record_counts_and_warns(self):
        report = bootstrap(
            OneSample([1.0, 2.0, 3.0]), self.fragile_stat,
            n_resamples=200, seed=1, methods=(PERCENTILE,),
            on_invalid="record",
        )
        self.assertGreater(report.invalid_resamples, 0)
        self.assertTrue(report.warnings)
        self.assertIn("无效", report.warnings[0])

    def test_bad_n_resamples_rejected(self):
        sample = OneSample([1.0, 2.0])
        for bad in (0, -5, 2.5, "100", True, None):
            with self.assertRaises((ValueError, TypeError)):
                bootstrap(sample, mean, n_resamples=bad, seed=1)


class TestBCaExplicitFailure(unittest.TestCase):
    def test_z0_infinite_fails(self):
        # min：重采样最小值恒 >= 观测最小值 → z0 无穷，显式失败
        with self.assertRaises(BCANotEstimableError):
            bootstrap(
                OneSample([3.0, 1.0, 2.0, 4.0]), min,
                n_resamples=500, seed=2, methods=(BCA,),
            )

    def test_degenerate_jackknife_fails(self):
        # 留一后不同值个数恒为 3 → jackknife 退化 → 加速度不可估
        stat = lambda v: float(len(set(v)))
        with self.assertRaises(AccelerationNotEstimableError):
            bootstrap(
                OneSample([1.0, 2.0, 3.0, 4.0]), stat,
                n_resamples=500, seed=3, methods=(BCA,),
            )


class TestGroupedAndStratified(unittest.TestCase):
    def test_two_sample_interval(self):
        sample = TwoSample(
            [1.0, 2.0, 3.0, 4.0], [3.0, 4.0, 5.0, 6.0], sample_id="grp"
        )
        report = bootstrap(
            sample, mean_diff, n_resamples=1000, seed=21,
            methods=(PERCENTILE, BCA),
        )
        self.assertEqual(report.sample_sizes, {"n_x": 4, "n_y": 4})
        iv = report.intervals[PERCENTILE]
        self.assertLess(iv.low, report.estimate)
        self.assertGreater(iv.high, report.estimate)

    def test_stratified_stays_within_strata(self):
        # 两层取值不相交：层0均值必须落在 [1, 2]
        sample = StratifiedSample(((1.0, 2.0), (100.0, 200.0)))
        report = bootstrap(
            sample, lambda strata: mean(strata[0]),
            n_resamples=500, seed=31, methods=(PERCENTILE,),
        )
        iv = report.intervals[PERCENTILE]
        self.assertGreaterEqual(iv.low, 1.0)
        self.assertLessEqual(iv.high, 2.0)


class TestMovingBlockBootstrap(unittest.TestCase):
    def setUp(self):
        self.series = TimeSeriesSample(
            tuple(float((i * 7) % 13) for i in range(30)), sample_id="ts"
        )

    def test_mbb_percentile(self):
        report = bootstrap(
            self.series, mean, n_resamples=500, seed=41,
            methods=(PERCENTILE,), block_length=5, boundary="circular",
        )
        self.assertEqual(report.sample_sizes, {"n": 30})
        iv = report.intervals[PERCENTILE]
        self.assertLess(iv.low, iv.high)

    def test_mbb_requires_block_length(self):
        with self.assertRaises(ValueError):
            bootstrap(self.series, mean, n_resamples=100, seed=1,
                      methods=(PERCENTILE,))

    def test_mbb_bca_fails_explicitly(self):
        with self.assertRaises(BCANotEstimableError):
            bootstrap(self.series, mean, n_resamples=100, seed=1,
                      methods=(BCA,), block_length=5)


if __name__ == "__main__":
    unittest.main()
