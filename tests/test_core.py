import math
import unittest

from bootstrap_ci import (
    DegenerateJackknifeError,
    IIDPlan,
    InvalidResampleError,
    MovingBlockPlan,
    PairedPlan,
    RandomSource,
    StratifiedPlan,
    UnsupportedMethodError,
    bootstrap,
)


def mean(values):
    return sum(values) / len(values)


class TestBasicEngine(unittest.TestCase):
    def test_seed_and_sample_size_recorded(self):
        plan = IIDPlan([1.0, 2.0, 3.0, 4.0, 5.0])
        result = bootstrap(plan, mean, n_resamples=200, rng=RandomSource(123))
        self.assertEqual(result.seed, 123)
        self.assertEqual(result.n_units, 5)
        self.assertEqual(result.n_resamples, 200)
        self.assertEqual(result.kind, "monte_carlo")
        self.assertEqual(result.method, "percentile")
        self.assertEqual(result.invalid_count, 0)
        self.assertEqual(result.effective_resamples, 200)

    def test_rng_must_be_injected(self):
        with self.assertRaises(TypeError):
            bootstrap(IIDPlan([1.0, 2.0]), mean, n_resamples=10, rng=None)

    def test_reproducible_for_same_seed(self):
        plan = IIDPlan([3.0, 1.0, 4.0, 1.0, 5.0, 9.0, 2.0, 6.0])
        r1 = bootstrap(plan, mean, n_resamples=500, rng=RandomSource(7))
        r2 = bootstrap(plan, mean, n_resamples=500, rng=RandomSource(7))
        self.assertEqual(r1, r2)

    def test_different_seed_usually_differs(self):
        plan = IIDPlan([3.0, 1.0, 4.0, 1.0, 5.0, 9.0, 2.0, 6.0])
        r1 = bootstrap(plan, mean, n_resamples=500, rng=RandomSource(7))
        r2 = bootstrap(plan, mean, n_resamples=500, rng=RandomSource(8))
        self.assertNotEqual((r1.lower, r1.upper), (r2.lower, r2.upper))

    def test_interval_contains_estimate_for_mean(self):
        plan = IIDPlan([float(i) for i in range(20)])
        result = bootstrap(plan, mean, n_resamples=2000, rng=RandomSource(1))
        self.assertLessEqual(result.lower, result.estimate)
        self.assertLessEqual(result.estimate, result.upper)

    def test_bca_method_runs_and_is_labelled(self):
        plan = IIDPlan([float(i * i % 7) for i in range(15)])
        result = bootstrap(plan, mean, n_resamples=1000,
                           rng=RandomSource(2), method="bca")
        self.assertEqual(result.method, "bca")
        self.assertLess(result.lower, result.upper)

    def test_bad_arguments(self):
        plan = IIDPlan([1.0, 2.0])
        with self.assertRaises(ValueError):
            bootstrap(plan, mean, n_resamples=1, rng=RandomSource(0))
        with self.assertRaises(ValueError):
            bootstrap(plan, mean, n_resamples=100, rng=RandomSource(0),
                      alpha=1.5)
        with self.assertRaises(ValueError):
            bootstrap(plan, mean, n_resamples=100, rng=RandomSource(0),
                      method="studentized")
        with self.assertRaises(ValueError):
            bootstrap(plan, mean, n_resamples=100, rng=RandomSource(0),
                      invalid_policy="shhh")


class TestDegenerateSamples(unittest.TestCase):
    def test_identical_values_give_point_interval(self):
        plan = IIDPlan([5.0] * 10)
        result = bootstrap(plan, mean, n_resamples=200, rng=RandomSource(0))
        self.assertTrue(result.degenerate)
        self.assertEqual(result.lower, 5.0)
        self.assertEqual(result.upper, 5.0)

    def test_identical_pairs_give_point_interval(self):
        plan = PairedPlan([(1.0, 3.0)] * 6)
        diff = lambda pairs: sum(a - b for a, b in pairs) / len(pairs)
        result = bootstrap(plan, diff, n_resamples=100, rng=RandomSource(0))
        self.assertTrue(result.degenerate)
        self.assertEqual((result.lower, result.upper), (-2.0, -2.0))

    def test_bca_on_identical_values_fails_explicitly(self):
        plan = IIDPlan([5.0] * 10)
        with self.assertRaises(DegenerateJackknifeError):
            bootstrap(plan, mean, n_resamples=100, rng=RandomSource(0),
                      method="bca")


class TestPairedAndStratified(unittest.TestCase):
    def test_paired_difference_interval(self):
        pairs = [(float(i), float(i) + 2.0 + (i % 3)) for i in range(12)]
        plan = PairedPlan(pairs)
        diff = lambda ps: sum(b - a for a, b in ps) / len(ps)
        result = bootstrap(plan, diff, n_resamples=2000, rng=RandomSource(4))
        self.assertEqual(result.plan, "paired")
        self.assertLessEqual(result.lower, 3.0)
        self.assertLessEqual(3.0, result.upper)

    def test_stratified_statistic(self):
        plan = StratifiedPlan({"ctrl": [1.0, 2.0, 3.0, 4.0],
                               "trt": [3.0, 4.0, 5.0, 6.0]})
        stat = lambda s: mean(s["trt"]) - mean(s["ctrl"])
        result = bootstrap(plan, stat, n_resamples=1000, rng=RandomSource(6))
        self.assertEqual(result.plan, "stratified")
        self.assertEqual(result.n_units, 8)
        self.assertLessEqual(result.lower, 2.0)
        self.assertLessEqual(2.0, result.upper)

    def test_stratified_bca_uses_delete_one_jackknife(self):
        plan = StratifiedPlan({"a": [1.0, 2.0, 3.0], "b": [4.0, 6.0, 8.0]})
        stat = lambda s: mean(s["b"]) - mean(s["a"])
        result = bootstrap(plan, stat, n_resamples=500, rng=RandomSource(8),
                           method="bca")
        self.assertEqual(result.method, "bca")


class TestMovingBlock(unittest.TestCase):
    def test_percentile_interval_on_correlated_series(self):
        # AR(1)-like series: strongly autocorrelated.
        series = []
        value = 0.0
        state = 12345
        for _ in range(60):
            state = (1103515245 * state + 12345) % (2 ** 31)
            value = 0.9 * value + (state / 2 ** 31 - 0.5)
            series.append(value)
        plan = MovingBlockPlan(series, block_length=5, boundary="circular")
        result = bootstrap(plan, mean, n_resamples=1000, rng=RandomSource(9))
        self.assertEqual(result.plan, "moving_block")
        self.assertLessEqual(result.lower, result.estimate)
        self.assertLessEqual(result.estimate, result.upper)

    def test_bca_fails_explicitly_for_moving_block(self):
        plan = MovingBlockPlan([1.0, 2.0, 3.0, 4.0], block_length=2)
        with self.assertRaises(UnsupportedMethodError):
            bootstrap(plan, mean, n_resamples=100, rng=RandomSource(0),
                      method="bca")


class TestInvalidReplicates(unittest.TestCase):
    def make_statistic(self):
        # Variance-like statistic: undefined (NaN) when the resample is
        # degenerate (all values equal).
        def stat(values):
            m = mean(values)
            if len(set(values)) == 1:
                return float("nan")
            return sum((v - m) ** 2 for v in values) / (len(values) - 1)
        return stat

    def test_raise_policy_aborts(self):
        plan = IIDPlan([1.0, 1.0, 2.0, 3.0])  # degenerate resamples are likely
        with self.assertRaises(InvalidResampleError) as ctx:
            bootstrap(plan, self.make_statistic(), n_resamples=500,
                      rng=RandomSource(0))
        self.assertIsNotNone(ctx.exception.replicate_id)

    def test_omit_policy_reports_count_explicitly(self):
        plan = IIDPlan([1.0, 1.0, 2.0, 3.0])
        result = bootstrap(plan, self.make_statistic(), n_resamples=500,
                           rng=RandomSource(0), invalid_policy="omit")
        self.assertGreater(result.invalid_count, 0)
        self.assertEqual(result.effective_resamples,
                         result.n_resamples - result.invalid_count)

    def test_all_invalid_still_fails(self):
        plan = IIDPlan([2.0, 2.0, 2.0])
        with self.assertRaises(InvalidResampleError):
            bootstrap(plan, self.make_statistic(), n_resamples=50,
                      rng=RandomSource(0), invalid_policy="omit")

    def test_raising_statistic_aborts_under_raise_policy(self):
        def bad(values):
            if len(set(values)) == 1:
                raise ZeroDivisionError("degenerate resample")
            return mean(values)
        plan = IIDPlan([1.0, 1.0, 2.0])
        with self.assertRaises(InvalidResampleError):
            bootstrap(plan, bad, n_resamples=200, rng=RandomSource(0))

    def test_non_finite_observed_statistic_fails(self):
        plan = IIDPlan([1.0, 2.0, 3.0])
        with self.assertRaises(InvalidResampleError):
            bootstrap(plan, lambda vs: math.inf, n_resamples=10,
                      rng=RandomSource(0))


if __name__ == "__main__":
    unittest.main()
