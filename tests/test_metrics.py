import math
import unittest
from collections import Counter
from decimal import Decimal

from equivstats import NullPolicy, Verdict, decide_entropy, distinct_count, entropy_interval
from equivstats.metrics import class_report


class TestDistinctCount(unittest.TestCase):
    def test_same_value_ten_times_counts_once(self):
        counter = Counter({"x": 10})
        self.assertEqual(distinct_count(counter, NullPolicy.IGNORE), 1)
        self.assertEqual(distinct_count(counter, NullPolicy.AS_CATEGORY), 1)

    def test_null_ignored_by_default(self):
        counter = Counter({"a": 3, None: 4})
        self.assertEqual(distinct_count(counter, NullPolicy.IGNORE), 1)

    def test_null_as_category_by_config(self):
        counter = Counter({"a": 3, None: 4})
        self.assertEqual(distinct_count(counter, NullPolicy.AS_CATEGORY), 2)

    def test_all_null_class_policy_dependent(self):
        counter = Counter({None: 5})
        self.assertEqual(distinct_count(counter, NullPolicy.IGNORE), 0)
        self.assertEqual(distinct_count(counter, NullPolicy.AS_CATEGORY), 1)


class TestEntropyInterval(unittest.TestCase):
    def assert_interval_contains(self, interval, expected, max_width=Decimal("1e-10")):
        self.assertLessEqual(interval.lo, Decimal(str(expected)))
        self.assertGreaterEqual(interval.hi, Decimal(str(expected)))
        self.assertLess(interval.width, max_width)

    def test_entropy_uses_configured_log_base(self):
        counter = Counter({"a": 5, "b": 5})
        self.assert_interval_contains(entropy_interval(counter, NullPolicy.IGNORE, 2), 1.0)
        for base, expected in ((10, math.log10(2)), (math.e, math.log(2))):
            interval = entropy_interval(counter, NullPolicy.IGNORE, base)
            self.assertAlmostEqual(float(interval.midpoint), expected, places=12)
            self.assertLess(interval.width, Decimal("1e-10"))

    def test_single_value_entropy_zero(self):
        counter = Counter({"x": 10})
        interval = entropy_interval(counter, NullPolicy.IGNORE, 2)
        self.assertEqual(interval.lo, 0)
        self.assertEqual(interval.hi, 0)

    def test_all_null_entropy_policy_dependent(self):
        counter = Counter({None: 5})
        self.assertEqual(entropy_interval(counter, NullPolicy.IGNORE, 2).hi, 0)
        self.assertEqual(entropy_interval(counter, NullPolicy.AS_CATEGORY, 2).hi, 0)

    def test_entropy_ignores_null_mass_under_ignore_policy(self):
        counter = Counter({"a": 5, "b": 5, None: 100})
        self.assert_interval_contains(entropy_interval(counter, NullPolicy.IGNORE, 2), 1.0)


class TestThresholdDecision(unittest.TestCase):
    def setUp(self):
        self.counter = Counter({"a": 5, "b": 5})  # H = 1.0 (base 2)

    def test_exact_threshold_is_undecided(self):
        _, verdict = decide_entropy(self.counter, NullPolicy.IGNORE, 2, 1.0)
        self.assertIs(verdict, Verdict.UNDECIDED)

    def test_below_threshold_passes(self):
        _, verdict = decide_entropy(self.counter, NullPolicy.IGNORE, 2, "0.99999999999999999999")
        self.assertIs(verdict, Verdict.PASS)

    def test_above_threshold_fails(self):
        _, verdict = decide_entropy(self.counter, NullPolicy.IGNORE, 2, "1.00000000000000000001")
        self.assertIs(verdict, Verdict.FAIL)

    def test_precision_escalation_resolves_close_threshold(self):
        # 阈值与真实熵仅差 1e-30：低精度区间无法区分，提升精度后可判定。
        threshold = Decimal("0.999999999999999999999999999999")  # 1 - 1e-30
        interval, verdict = decide_entropy(
            self.counter, NullPolicy.IGNORE, 2, threshold,
            start_precision=28, max_precision=120)
        self.assertIs(verdict, Verdict.PASS)
        self.assertLess(interval.width, Decimal("1e-28"))


class TestClassReport(unittest.TestCase):
    def test_failing_class_and_contributions(self):
        counter = Counter({"x": 8, "y": 1, "z": 1})
        report = class_report(("q",), counter, NullPolicy.IGNORE, 2,
                              min_distinct=2, min_entropy=1.0)
        self.assertTrue(report.failing)
        self.assertTrue(report.distinct_ok)
        self.assertIs(report.entropy_verdict, Verdict.FAIL)
        self.assertEqual([c.count for c in report.contributions], [8, 1, 1])
        total = sum(c.entropy_contribution.midpoint for c in report.contributions)
        self.assertAlmostEqual(float(total), float(report.entropy.midpoint), places=8)

    def test_passing_class(self):
        counter = Counter({"x": 5, "y": 5})
        report = class_report(("q",), counter, NullPolicy.IGNORE, 2,
                              min_distinct=2, min_entropy=0.5)
        self.assertFalse(report.failing)
        self.assertIs(report.entropy_verdict, Verdict.PASS)


if __name__ == "__main__":
    unittest.main()
