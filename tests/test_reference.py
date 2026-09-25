"""Cross-check the analytical mapper against brute-force enumeration in a
small rational domain."""

import unittest
from fractions import Fraction

from clockmap import (
    Anchor,
    EpochMapper,
    PiecewiseClockMapper,
    SlopeBounds,
    WallClock,
    check_epochs_independent,
    reference_band,
    reference_feasible,
)

SLOPES = SlopeBounds.from_nominal_rate(10, Fraction(1, 10))


def anchor(epoch, index, lo, hi, label=""):
    return Anchor(epoch, index, WallClock(Fraction(lo)), WallClock(Fraction(hi)), label)


def small_case_anchors():
    # narrow intervals keep the brute-force grid small and exact
    return [
        anchor(0, 0, 100, Fraction(1001, 10), "a0"),
        anchor(0, 100, 110, Fraction(1101, 10), "a1"),
    ]


class TestReferenceCrossCheck(unittest.TestCase):
    def setUp(self):
        self.anchors = small_case_anchors()
        self.epoch = EpochMapper(0, SLOPES)
        self.epoch.anchors = list(self.anchors)

    def test_band_matches_brute_force(self):
        for index in (0, 25, 50, 75, 100, 130):
            lo, hi = self.epoch.band_at(index)
            ref = reference_band(self.anchors, SLOPES, index, quantum=Fraction(1, 100))
            self.assertIsNotNone(ref)
            self.assertEqual((lo, hi), ref, f"band mismatch at index {index}")

    def test_feasibility_matches_brute_force(self):
        self.assertTrue(self.epoch.feasible())
        self.assertTrue(reference_feasible(self.anchors, SLOPES))

    def test_infeasible_matches_brute_force(self):
        bad = self.anchors + [anchor(0, 200, 1000, 1001, "a2")]
        self.epoch.anchors = bad
        self.assertFalse(self.epoch.feasible())
        self.assertFalse(reference_feasible(bad, SLOPES))
        self.assertIsNone(reference_band(bad, SLOPES, 50))

    def test_dropped_index_band_matches_brute_force(self):
        # indices 0 and 100 calibrated; 50 was dropped from the stream
        lo, hi = self.epoch.band_at(50)
        ref = reference_band(self.anchors, SLOPES, 50, quantum=Fraction(1, 100))
        self.assertEqual((lo, hi), ref)
        self.assertLess(lo, hi)  # the dropped interval genuinely exists


class TestEpochIndependenceReference(unittest.TestCase):
    def test_rollback_epochs_independently_feasible(self):
        epoch0 = [anchor(0, 0, 1000, 1001), anchor(0, 100, 1010, 1011)]
        epoch1 = [anchor(1, 0, 50, 51), anchor(1, 100, 60, 61)]  # rolled back
        self.assertTrue(check_epochs_independent({0: epoch0, 1: epoch1}, SLOPES))
        mapper = PiecewiseClockMapper(10, SLOPES)
        for a in epoch0 + epoch1:
            mapper.add_anchor(a)
        self.assertEqual(mapper.status(0), "ok")
        self.assertEqual(mapper.status(1), "ok")


if __name__ == "__main__":
    unittest.main()
