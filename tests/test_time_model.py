"""Tests for the exact integer time model (wall clock / point / duration)."""

import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from tempnet import (
    Duration,
    TimePoint,
    WallClock,
    UnitTable,
    MICROSECOND_TABLE,
)


class TestSyntheticTable(unittest.TestCase):
    def test_exact_integer_conversions(self):
        t = MICROSECOND_TABLE
        self.assertEqual(t.ticks(1, "second"), 1_000_000)
        self.assertEqual(t.ticks(1, "day"), 86_400_000_000)
        self.assertEqual(t.in_unit(2_500_000, "millisecond"), 2500)
        self.assertEqual(t.in_unit(3_000_000, "second"), 3)

    def test_fractional_conversion_refused_not_rounded(self):
        t = MICROSECOND_TABLE
        with self.assertRaises(ValueError):
            t.in_unit(1_500, "second")

    def test_float_quantities_rejected(self):
        with self.assertRaises(TypeError):
            MICROSECOND_TABLE.ticks(1.5, "second")  # type: ignore[arg-type]

    def test_custom_synthetic_table(self):
        table = UnitTable("slot", {"cycle": ("slot", 12), "frame": ("cycle", 8)})
        self.assertEqual(table.ticks(1, "frame"), 96)
        self.assertEqual(table.in_unit(192, "cycle"), 16)

    def test_parent_must_precede_child(self):
        with self.assertRaises(ValueError):
            UnitTable("a", {"c": ("b", 2), "b": ("a", 3)})


class TestPointDurationWallClock(unittest.TestCase):
    def test_duration_arithmetic_is_integer(self):
        d1 = Duration(3_000_000)
        d2 = Duration(500_000)
        self.assertEqual((d1 + d2).ticks, 3_500_000)
        self.assertEqual((d1 - d2).ticks, 2_500_000)
        self.assertEqual((-d2).ticks, -500_000)
        with self.assertRaises(TypeError):
            Duration(1.0)  # type: ignore[arg-type]

    def test_point_difference_is_duration_not_point(self):
        a = TimePoint(10)
        b = TimePoint(13)
        gap = b - a
        self.assertIsInstance(gap, Duration)
        self.assertEqual(gap.ticks, 3)
        self.assertIsInstance(a + Duration(2), TimePoint)

    def test_wall_clock_needs_table_and_epoch(self):
        p = TimePoint(86_400_000_000 + 1_000_000)
        w = p.to_wall(MICROSECOND_TABLE, epoch_ticks=0, epoch_label="day0")
        self.assertIsInstance(w, WallClock)
        self.assertEqual((w - WallClock(0, MICROSECOND_TABLE)).ticks,
                         86_401_000_000)
        self.assertTrue(w.format().startswith("0001 00:00:01"))

    def test_wall_vs_point_are_distinct_types(self):
        self.assertNotEqual(TimePoint(5), WallClock(5, MICROSECOND_TABLE))
        with self.assertRaises(TypeError):
            TimePoint(1) + 1  # type: ignore[operator]

    def test_integrating_with_network_kept_exact(self):
        from tempnet import MinMaxNetwork
        table = MICROSECOND_TABLE
        net = MinMaxNetwork(["A", "B"])
        # B-A between 1.5 s and 2 s expressed as exact integer micros.
        net.add_interval(
            "gap", "A", "B",
            table.ticks(1, "second") + table.ticks(500, "millisecond"),
            table.ticks(2, "second"),
        )
        net.anchor("A", 0)
        b = net.bounds("B")
        self.assertEqual(b.earliest, 1_500_000)
        self.assertEqual(b.latest, 2_000_000)
        # Earliest B is exactly 1 second 500 ms after the wall origin.
        wall_b = TimePoint(b.earliest).to_wall(table, epoch_label="t0")
        self.assertEqual((wall_b - WallClock(0, table)).in_unit(table, "millisecond"),
                         1500)


if __name__ == "__main__":
    unittest.main(verbosity=2)
