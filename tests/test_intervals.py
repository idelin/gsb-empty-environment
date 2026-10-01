import math
import unittest

from intervals import Decision, Interval, decide_at_least
from metrics import entropy_interval_of_counts


class TestIntervalArithmetic(unittest.TestCase):
    def test_outward_rounding_contains_true_value(self):
        a = Interval.point(0.1)
        b = Interval.point(0.2)
        s = a + b
        self.assertLessEqual(s.lo, 0.3)
        self.assertGreaterEqual(s.hi, 0.3)
        self.assertGreater(s.width, 0.0)

    def test_mul_div_log(self):
        x = Interval.point(2.0) / Interval.point(3.0)
        self.assertLessEqual(x.lo, 2.0 / 3.0)
        self.assertGreaterEqual(x.hi, 2.0 / 3.0)
        lg = x.log()
        self.assertLessEqual(lg.lo, math.log(2.0 / 3.0))
        self.assertGreaterEqual(lg.hi, math.log(2.0 / 3.0))

    def test_decide_clear_cases(self):
        self.assertIs(decide_at_least(Interval(1.0, 1.1), 0.5), Decision.SATISFIED)
        self.assertIs(decide_at_least(Interval(0.1, 0.2), 0.5), Decision.VIOLATED)
        self.assertIs(decide_at_least(Interval(0.4, 0.6), 0.5), Decision.UNDECIDED)


class TestEntropyIntervalDecision(unittest.TestCase):
    def test_exact_threshold_is_undecided(self):
        # 50/50 二值分布在 base 2 下熵恰为 1.0，区间仍含阈值 -> 未决
        interval = entropy_interval_of_counts({"a": 50, "b": 50}, 2.0)
        self.assertIs(decide_at_least(interval, 1.0), Decision.UNDECIDED)
        self.assertIs(decide_at_least(interval, 0.999), Decision.SATISFIED)
        self.assertIs(decide_at_least(interval, 1.001), Decision.VIOLATED)

    def test_interval_contains_float_entropy(self):
        counts = {"a": 7, "b": 3, "c": 2}
        interval = entropy_interval_of_counts(counts, 2.0)
        n = 12
        exact = -sum((c / n) * math.log2(c / n) for c in counts.values())
        self.assertLessEqual(interval.lo, exact)
        self.assertGreaterEqual(interval.hi, exact)

    def test_entropy_interval_non_negative(self):
        interval = entropy_interval_of_counts({"a": 5}, 2.0)
        self.assertGreaterEqual(interval.lo, 0.0)


if __name__ == "__main__":
    unittest.main()
