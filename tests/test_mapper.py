"""Core mapper behaviour: bands, tables, conflicts, epochs, revisions."""

import unittest
from fractions import Fraction

from clockmap import (
    Anchor,
    PiecewiseClockMapper,
    SlopeBounds,
    WallClock,
    find_drops,
)

# nominal 10 Hz, +/-10% drift  ->  slope in [9/100, 11/100] s/sample
SLOPES = SlopeBounds.from_nominal_rate(10, Fraction(1, 10))
S_MIN = Fraction(9, 100)
S_MAX = Fraction(11, 100)


def anchor(epoch, index, lo, hi, label=""):
    return Anchor(epoch, index, WallClock(lo), WallClock(hi), label)


def two_anchor_mapper():
    mapper = PiecewiseClockMapper(10, SLOPES)
    mapper.add_anchor(anchor(0, 0, 100, 101, "a0"))
    mapper.add_anchor(anchor(0, 100, 110, 111, "a1"))
    return mapper


class TestBands(unittest.TestCase):
    def test_band_between_anchors(self):
        mapper = two_anchor_mapper()
        iv = mapper.map_index(0, 50)
        self.assertEqual(iv.earliest.seconds, Fraction(209, 2))  # 104.5
        self.assertEqual(iv.latest.seconds, Fraction(213, 2))    # 106.5
        self.assertTrue(iv.feasible)

    def test_band_extrapolation(self):
        mapper = two_anchor_mapper()
        iv = mapper.map_index(0, 150)
        self.assertEqual(iv.earliest.seconds, Fraction(229, 2))  # 114.5
        self.assertEqual(iv.latest.seconds, Fraction(233, 2))    # 116.5

    def test_band_at_anchor_is_within_calibration(self):
        mapper = two_anchor_mapper()
        iv = mapper.map_index(0, 0)
        self.assertEqual((iv.earliest.seconds, iv.latest.seconds), (100, 101))

    def test_error_intervals_not_points(self):
        mapper = two_anchor_mapper()
        iv = mapper.map_index(0, 50)
        self.assertGreater(iv.width.seconds, 0)


class TestConversionTable(unittest.TestCase):
    def test_table_covers_all_indices_and_matches_band(self):
        mapper = two_anchor_mapper()
        table = mapper.conversion_table(0)
        self.assertEqual(len(table), 3)  # (-inf,0], [0,100], [100,+inf)
        for index in (0, 1, 25, 50, 99, 100, 101, 500):
            matching = [s for s in table if s.contains(index)]
            self.assertTrue(matching, f"no segment covers {index}")
            lo, hi = matching[0].band_at(index)
            iv = mapper.map_index(0, index)
            self.assertEqual(lo, iv.earliest.seconds)
            self.assertEqual(hi, iv.latest.seconds)

    def test_table_is_piecewise_with_exact_offsets(self):
        mapper = two_anchor_mapper()
        table = mapper.conversion_table(0)
        middle = next(s for s in table if s.first_index == 0 and s.last_index == 100)
        slopes = {line.slope for line in middle.lower.lines}
        self.assertEqual(slopes, {S_MIN, S_MAX})
        for segment in table:
            for line in segment.lower.lines + segment.upper.lines:
                self.assertIsInstance(line.offset, Fraction)


class TestDrops(unittest.TestCase):
    def test_find_drops(self):
        observed = list(range(0, 50)) + list(range(59, 100))
        self.assertEqual(find_drops(observed), [(50, 58, 9)])

    def test_dropped_interval_still_exists(self):
        mapper = PiecewiseClockMapper(10, SLOPES)
        mapper.add_anchor(anchor(0, 0, 1000, 1001))
        # jump of 10: samples 50..58 dropped, time interval remains
        lo49, hi49 = mapper.map_index(0, 49).earliest.seconds, mapper.map_index(0, 49).latest.seconds
        iv59 = mapper.map_index(0, 59)
        self.assertEqual(iv59.earliest.seconds - lo49, 10 * S_MIN)
        self.assertEqual(iv59.latest.seconds - hi49, 10 * S_MAX)
        # a dropped index itself still maps to a well-defined interval
        iv54 = mapper.map_index(0, 54)
        self.assertTrue(iv54.feasible)
        self.assertEqual(iv54.earliest.seconds, 1000 + 54 * S_MIN)


class TestConflicts(unittest.TestCase):
    def build_conflicted(self):
        mapper = two_anchor_mapper()
        mapper.add_anchor(anchor(0, 200, 1000, 1001, "a2"))
        return mapper

    def test_inconsistent_calibration_flagged_anomaly(self):
        mapper = self.build_conflicted()
        self.assertEqual(mapper.status(0), "anomaly")
        self.assertFalse(mapper.revisions[-1].feasible)

    def test_minimal_conflict_set(self):
        mapper = self.build_conflicted()
        conflict = mapper.conflict_set(0)
        self.assertEqual({a.label for a in conflict}, {"a1", "a2"})
        # irreducible: removing either one from the conflict set is feasible
        epoch = mapper.epochs[0]
        for a in conflict:
            rest = [x for x in conflict if x is not a]
            self.assertTrue(epoch.feasible(rest))

    def test_revision_carries_conflict_set(self):
        mapper = self.build_conflicted()
        revision = mapper.revisions[-1]
        self.assertEqual({a.label for a in revision.conflict_set}, {"a1", "a2"})


class TestEpochs(unittest.TestCase):
    def test_epochs_never_bridged(self):
        mapper = two_anchor_mapper()
        before = mapper.map_index(0, 50)
        mapper.add_anchor(anchor(1, 0, 5000, 5001, "b0"))
        after = mapper.map_index(0, 50)
        self.assertEqual(before, after)  # epoch 1 anchor does not leak into epoch 0
        iv = mapper.map_index(1, 0)
        self.assertEqual((iv.earliest.seconds, iv.latest.seconds), (5000, 5001))

    def test_reset_indices_not_joined_to_old_epoch(self):
        mapper = two_anchor_mapper()
        mapper.add_anchor(anchor(1, 0, 5000, 5001))
        mapper.add_anchor(anchor(1, 10, 5001, 5002))
        iv = mapper.map_index(1, 5)  # same index value exists in epoch 0 too
        self.assertEqual(iv.epoch, 1)
        self.assertGreater(iv.earliest.seconds, 4999)

    def test_unknown_epoch_rejected(self):
        mapper = two_anchor_mapper()
        with self.assertRaises(KeyError):
            mapper.map_index(7, 0)

    def test_rollback_detected_and_allowed(self):
        mapper = PiecewiseClockMapper(10, SLOPES)
        mapper.add_anchor(anchor(0, 0, 1000, 1001))
        mapper.add_anchor(anchor(0, 100, 1010, 1011))
        revision = mapper.add_anchor(anchor(1, 0, 50, 51))  # clock reset backwards
        self.assertTrue(revision.new_epoch)
        self.assertTrue(revision.rollback_detected)
        self.assertEqual(mapper.status(1), "ok")


class TestLateCalibration(unittest.TestCase):
    def test_late_anchor_revises_mapping_and_keeps_evidence(self):
        mapper = two_anchor_mapper()
        old = mapper.map_index(0, 50)
        self.assertEqual(old.earliest.seconds, Fraction(209, 2))
        # late calibration: tighter knowledge at index 50
        revision = mapper.add_anchor(anchor(0, 50, 105, 106, "late"))
        new = mapper.map_index(0, 50)
        self.assertEqual((new.earliest.seconds, new.latest.seconds), (105, 106))
        # exactly one prior result was revised
        self.assertEqual(len(revision.updates), 1)
        record, revised = revision.updates[0]
        self.assertEqual(record.interval, old)
        self.assertEqual(revised, new)
        self.assertTrue(record.superseded)
        self.assertEqual(record.replaced_by, new)
        # evidence is kept, not deleted
        self.assertIn(record, mapper.evidence)

    def test_unaffected_epochs_not_revised(self):
        mapper = two_anchor_mapper()
        mapper.add_anchor(anchor(1, 0, 5000, 5001))
        mapper.map_index(1, 0)
        revision = mapper.add_anchor(anchor(0, 50, 105, 106))
        self.assertEqual(revision.updates, [])


if __name__ == "__main__":
    unittest.main()
