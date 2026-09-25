"""Reference verification in a small rational domain.

All expected values below are hand-computed with exact fractions so the
tests act as an independent check of the implementation, including dropped
samples, wall-clock rollback, and neighbouring huge timestamps that float64
would collapse into one value.
"""
from __future__ import annotations

import unittest
from fractions import Fraction

from clockmap import (
    Anchor,
    ClockMapper,
    ConflictError,
    Duration,
    EpochBoundaryError,
    NoAnchorsError,
    OffsetTable,
    SlopeModel,
    WallTime,
)

# 10 Hz nominal, period bounded to [9/100, 11/100] s per sample.
SLOPE = SlopeModel(Fraction(1, 10), Fraction(9, 100), Fraction(11, 100))


def anchor(epoch, seq, lo, hi, label=""):
    return Anchor(epoch, seq, Fraction(lo), Fraction(hi), label)


class TypeSafetyTests(unittest.TestCase):
    def test_wall_time_plus_wall_time_rejected(self):
        with self.assertRaises(TypeError):
            WallTime(1) + WallTime(2)

    def test_wall_time_minus_wall_time_is_duration(self):
        delta = WallTime(7) - WallTime(3)
        self.assertIsInstance(delta, Duration)
        self.assertEqual(delta, Duration(4))

    def test_wall_time_plus_duration_commutes(self):
        self.assertEqual(WallTime(3) + Duration(2), WallTime(5))
        self.assertEqual(Duration(2) + WallTime(3), WallTime(5))

    def test_float_rejected_everywhere(self):
        with self.assertRaises(TypeError):
            WallTime(1.5)
        with self.assertRaises(TypeError):
            Duration(0.1)
        with self.assertRaises(TypeError):
            Anchor(0, 0, 1.0, 2.0)

    def test_huge_neighbouring_timestamps_not_swallowed(self):
        base = Fraction(2**60)
        a = WallTime(base + Fraction(1, 2**30))
        b = WallTime(base + Fraction(2, 2**30))
        # float64 mantissa cannot resolve 2**-30 near 2**60 (ulp is 256).
        self.assertEqual(float(a), float(b))
        self.assertNotEqual(a, b)
        self.assertEqual(b - a, Duration(Fraction(1, 2**30)))


class FeasibleIntervalTests(unittest.TestCase):
    def test_single_anchor_forward(self):
        mapper = ClockMapper(SLOPE)
        mapper.add_anchor(anchor(0, 0, 10, 10, "A"))
        iv = mapper.feasible_interval(0, 100)
        self.assertEqual(iv.lo.seconds, Fraction(19))
        self.assertEqual(iv.hi.seconds, Fraction(21))

    def test_single_anchor_backward(self):
        mapper = ClockMapper(SLOPE)
        mapper.add_anchor(anchor(0, 100, 10, 10, "A"))
        iv = mapper.feasible_interval(0, 0)
        # 10 - 100*11/100 = -1 ; 10 - 100*9/100 = 1
        self.assertEqual(iv.lo.seconds, Fraction(-1))
        self.assertEqual(iv.hi.seconds, Fraction(1))

    def test_two_anchors_intersect(self):
        mapper = ClockMapper(SLOPE)
        mapper.add_anchor(anchor(0, 0, 10, 10, "A"))
        mapper.add_anchor(anchor(0, 200, 29, 31, "B"))
        iv = mapper.feasible_interval(0, 100)
        # A gives [19, 21]; B gives [29-11, 31-9] = [18, 22]; meet is [19, 21].
        self.assertEqual(iv.lo.seconds, Fraction(19))
        self.assertEqual(iv.hi.seconds, Fraction(21))

    def test_anchor_error_intervals_widen_result(self):
        mapper = ClockMapper(SLOPE)
        mapper.add_anchor(anchor(0, 0, 10, Fraction(21, 2), "A"))
        iv = mapper.feasible_interval(0, 10)
        self.assertEqual(iv.lo.seconds, Fraction(109, 10))
        self.assertEqual(iv.hi.seconds, Fraction(232, 20))

    def test_unknown_epoch_raises(self):
        mapper = ClockMapper(SLOPE)
        with self.assertRaises(NoAnchorsError):
            mapper.feasible_interval(99, 0)


class DroppedSampleTests(unittest.TestCase):
    def test_seq_jump_of_10_keeps_time_interval(self):
        mapper = ClockMapper(SLOPE)
        mapper.add_anchor(anchor(0, 2000, 200, 200, "A"))
        # Samples 2000 and 2010: nine samples dropped, ten periods elapse.
        lo, hi = mapper.duration_between(0, 2000, 0, 2010)
        self.assertEqual(lo, Duration(Fraction(9, 10)))
        self.assertEqual(hi, Duration(Fraction(11, 10)))

    def test_duration_uses_anchor_tightened_bounds(self):
        mapper = ClockMapper(SLOPE)
        mapper.add_anchor(anchor(0, 0, 10, 10, "A"))
        mapper.add_anchor(anchor(0, 100, 20, 20, "B"))
        lo, hi = mapper.duration_between(0, 0, 0, 100)
        # Slope alone: [9, 11]; anchors pin it to exactly 10.
        self.assertEqual(lo, Duration(10))
        self.assertEqual(hi, Duration(10))


class EpochTests(unittest.TestCase):
    def test_duration_across_epochs_rejected(self):
        mapper = ClockMapper(SLOPE)
        mapper.add_anchor(anchor(0, 5, 10, 10, "A"))
        mapper.add_anchor(anchor(1, 5, 99, 99, "B"))
        with self.assertRaises(EpochBoundaryError):
            mapper.duration_between(0, 5, 1, 5)

    def test_reset_epoch_does_not_inherit_old_sequence(self):
        mapper = ClockMapper(SLOPE)
        mapper.add_anchor(anchor(0, 1000, 100, 100, "old"))
        mapper.add_anchor(anchor(1, 3, 500, 500, "new"))
        # After reset, seq 3 of epoch 1 must not be reachable from epoch 0.
        iv = mapper.feasible_interval(1, 3)
        self.assertEqual(iv.lo.seconds, Fraction(500))
        self.assertEqual(iv.hi.seconds, Fraction(500))
        # Epoch 0 anchors never leak into epoch 1 queries.
        iv = mapper.feasible_interval(1, 13)
        self.assertEqual(iv.lo.seconds, Fraction(5009, 10))
        self.assertEqual(iv.hi.seconds, Fraction(5011, 10))


class ConflictTests(unittest.TestCase):
    def test_rollback_flagged_and_minimal_set(self):
        mapper = ClockMapper(SLOPE)
        a = anchor(0, 0, 10, 10, "A")
        b = anchor(0, 100, 5, 5, "B")  # wall clock went backwards
        self.assertEqual(mapper.add_anchor(a), [])
        conflicts = mapper.add_anchor(b)
        self.assertEqual(len(conflicts), 1)
        self.assertIn("rollback", conflicts[0].reason)
        self.assertTrue(mapper.is_anomalous(b))
        minimal = mapper.minimal_conflict_set(0)
        self.assertEqual(set(minimal), {a, b})

    def test_too_fast_flagged(self):
        mapper = ClockMapper(SLOPE)
        mapper.add_anchor(anchor(0, 0, 10, 10, "A"))
        conflicts = mapper.add_anchor(anchor(0, 100, 50, 50, "B"))
        self.assertEqual(len(conflicts), 1)
        self.assertIn("faster", conflicts[0].reason)

    def test_self_inconsistent_anchor_is_singleton_conflict(self):
        mapper = ClockMapper(SLOPE)
        bad = anchor(0, 0, 12, 10, "bad")
        mapper.add_anchor(bad)
        self.assertEqual(mapper.minimal_conflict_set(0), (bad,))

    def test_inconsistent_calibration_makes_query_infeasible(self):
        mapper = ClockMapper(SLOPE)
        mapper.add_anchor(anchor(0, 0, 10, 10, "A"))
        mapper.add_anchor(anchor(0, 100, 5, 5, "B"))
        with self.assertRaises(ConflictError) as ctx:
            mapper.map_seq(0, 50)
        self.assertEqual(len(ctx.exception.anchors), 2)

    def test_three_anchors_one_outlier(self):
        mapper = ClockMapper(SLOPE)
        good1 = anchor(0, 0, 10, 10, "good1")
        good2 = anchor(0, 100, 20, 20, "good2")
        bad = anchor(0, 50, 99, 99, "bad")
        mapper.add_anchor(good1)
        mapper.add_anchor(good2)
        mapper.add_anchor(bad)
        self.assertTrue(mapper.is_anomalous(bad))
        self.assertFalse(mapper.is_anomalous(good1))
        # Every recorded conflict involves the outlier.
        for conflict in mapper.conflicts(0):
            self.assertIn(bad, (conflict.first, conflict.second))


class LateCalibrationTests(unittest.TestCase):
    def test_late_anchor_revises_mapping_and_keeps_evidence(self):
        mapper = ClockMapper(SLOPE)
        mapper.add_anchor(anchor(0, 0, 10, 10, "A"))
        first = mapper.map_seq(0, 1000)
        self.assertEqual(first.interval.lo.seconds, Fraction(100))
        self.assertEqual(first.interval.hi.seconds, Fraction(120))
        # Late calibration arrives near the query point.
        mapper.add_anchor(anchor(0, 900, 105, 105, "late"))
        revised = mapper.map_seq(0, 1000)
        self.assertEqual(revised.interval.lo.seconds, Fraction(114))
        self.assertEqual(revised.interval.hi.seconds, Fraction(116))
        # The earlier result is superseded but preserved as evidence.
        self.assertTrue(first.superseded)
        self.assertFalse(revised.superseded)
        self.assertIs(revised.supersedes, first)
        self.assertEqual(mapper.estimates(0, 1000), (first, revised))
        self.assertIn("late", revised.evidence)
        self.assertNotIn("late", first.evidence)

    def test_repeat_query_without_new_anchor_returns_same_estimate(self):
        mapper = ClockMapper(SLOPE)
        mapper.add_anchor(anchor(0, 0, 10, 10, "A"))
        first = mapper.map_seq(0, 10)
        self.assertIs(mapper.map_seq(0, 10), first)


class OffsetTableTests(unittest.TestCase):
    def test_exact_conversion_between_control_points(self):
        table = OffsetTable(Fraction(1, 10))
        table.add(0, 0, 10, 10)
        table.add(0, 1000, 100, 100)
        iv = table.to_wall(0, 500)
        self.assertEqual(iv.lo.seconds, Fraction(55))
        self.assertEqual(iv.hi.seconds, Fraction(55))

    def test_round_trip_is_exact(self):
        table = OffsetTable(Fraction(1, 10))
        table.add(0, 0, 10, 10)
        table.add(0, 1000, 100, 100)
        iv = table.to_wall(0, 123)
        lo, hi = table.to_seq(0, iv.lo)
        self.assertLessEqual(lo, 123)
        self.assertGreaterEqual(hi, 123)
        self.assertEqual(lo, hi)  # zero-width table converts back exactly

    def test_from_mapper_and_uncertainty(self):
        mapper = ClockMapper(SLOPE)
        mapper.add_anchor(anchor(0, 0, 10, 11, "A"))
        mapper.add_anchor(anchor(0, 100, 20, 22, "B"))
        table = OffsetTable.from_mapper(mapper, 0)
        iv = table.to_wall(0, 50)
        # Offsets: A -> [10, 11], B -> [10, 12]; midpoint offsets [10, 23/2].
        self.assertEqual(iv.lo.seconds, Fraction(15))
        self.assertEqual(iv.hi.seconds, Fraction(33, 2))

    def test_offset_table_never_uses_float(self):
        table = OffsetTable(Fraction(1, 10))
        with self.assertRaises(TypeError):
            table.add(0, 0, 1.5, 2.0)


if __name__ == "__main__":
    unittest.main()
