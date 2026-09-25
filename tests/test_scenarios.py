"""End-to-end scenarios: drops, rollback, huge nearby timestamps, anomalies."""

import unittest
from fractions import Fraction

from clockmap import (
    Anchor,
    PiecewiseClockMapper,
    SlopeBounds,
    WallClock,
    find_drops,
)

SLOPES = SlopeBounds.from_nominal_rate(10, Fraction(1, 10))
S_MIN = Fraction(9, 100)
S_MAX = Fraction(11, 100)


def anchor(epoch, index, lo, hi, label=""):
    return Anchor(epoch, index, WallClock(Fraction(lo)), WallClock(Fraction(hi)), label)


class TestDroppedSamples(unittest.TestCase):
    def test_jump_of_ten_keeps_its_time_interval(self):
        mapper = PiecewiseClockMapper(10, SLOPES)
        mapper.add_anchor(anchor(0, 0, 1000, 1001))
        observed = list(range(0, 50)) + list(range(59, 100))
        drops = find_drops(observed)
        self.assertEqual(drops, [(50, 58, 9)])
        # 9 samples dropped, but the jump of 10 still spans 10 periods on
        # both the lower and the upper bound track
        iv49 = mapper.map_index(0, 49)
        iv59 = mapper.map_index(0, 59)
        self.assertEqual((iv59.earliest - iv49.earliest).seconds, 10 * S_MIN)
        self.assertEqual((iv59.latest - iv49.latest).seconds, 10 * S_MAX)
        # the dropped indices themselves map to well-defined intervals
        for missing in range(50, 59):
            self.assertTrue(mapper.map_index(0, missing).feasible)


class TestRollback(unittest.TestCase):
    def test_reset_clock_may_run_behind_and_stays_separate(self):
        mapper = PiecewiseClockMapper(10, SLOPES)
        mapper.add_anchor(anchor(0, 0, 10**6, 10**6 + 1))
        mapper.add_anchor(anchor(0, 100, 10**6 + 10, 10**6 + 11))
        revision = mapper.add_anchor(anchor(1, 0, 100, 101))
        self.assertTrue(revision.rollback_detected)
        # epoch 1 maps by its own anchors only; no bridging to epoch 0
        iv = mapper.map_index(1, 10)
        self.assertEqual(iv.earliest.seconds, 100 + 10 * S_MIN)
        self.assertLess(iv.latest.seconds, 10**6)


class TestHugeNearbyTimestamps(unittest.TestCase):
    def test_microseconds_at_1e18_seconds_stay_distinct(self):
        base = 10**18
        us = Fraction(1, 10**6)
        mapper = PiecewiseClockMapper(10, SLOPES)
        mapper.add_anchor(anchor(0, 0, base + us, base + 2 * us))
        mapper.add_anchor(anchor(0, 100, base + 10 + us, base + 10 + 2 * us))
        iv = mapper.map_index(0, 50)
        lo = iv.earliest.seconds
        hi = iv.latest.seconds
        self.assertEqual(lo, base + us + 50 * S_MIN)
        self.assertEqual(hi, base + 2 * us + 50 * S_MAX)
        self.assertEqual(hi - lo, us + 50 * (S_MAX - S_MIN))
        # exact arithmetic keeps the microsecond-level width; float64 would
        # swallow both endpoints into the same number (ulp at 1e18 is 256)
        self.assertEqual(float(lo), float(hi))
        self.assertGreater(hi - lo, 0)


class TestAnomalyFlow(unittest.TestCase):
    def test_inconsistent_calibration_marks_anomaly_with_conflict(self):
        mapper = PiecewiseClockMapper(10, SLOPES)
        mapper.add_anchor(anchor(0, 0, 100, 101, "first"))
        mapper.add_anchor(anchor(0, 100, 110, 111, "second"))
        self.assertEqual(mapper.status(0), "ok")
        revision = mapper.add_anchor(anchor(0, 200, 1000, 1001, "liar"))
        self.assertEqual(mapper.status(0), "anomaly")
        self.assertFalse(revision.feasible)
        self.assertEqual({a.label for a in revision.conflict_set}, {"second", "liar"})
        # earlier evidence is still on record
        self.assertEqual(len(mapper.revisions), 3)


if __name__ == "__main__":
    unittest.main()
