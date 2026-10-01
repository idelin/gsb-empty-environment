import datetime
import unittest
from fractions import Fraction

from timecore import (
    AffineCalibration,
    CalibrationGraph,
    ConversionTable,
    Duration,
    ErrorBudget,
    MICROSECOND,
    MILLISECOND,
    NANOSECOND,
    SAMPLE_48K,
    SECOND,
    RoundingPolicy,
    TimePoint,
    WallClockTime,
    round_rational,
)


class TestRounding(unittest.TestCase):
    def test_third_of_second_to_microseconds_half_even(self):
        third = Duration.of(Fraction(1, 3), SECOND)
        self.assertEqual(third.to(MICROSECOND), Fraction(1_000_000, 3))
        self.assertEqual(
            third.to_int(MICROSECOND, RoundingPolicy.HALF_EVEN), 333333
        )

    def test_half_even_ties(self):
        self.assertEqual(round_rational(Fraction(1, 2), RoundingPolicy.HALF_EVEN), 0)
        self.assertEqual(round_rational(Fraction(3, 2), RoundingPolicy.HALF_EVEN), 2)
        self.assertEqual(round_rational(Fraction(5, 2), RoundingPolicy.HALF_EVEN), 2)
        self.assertEqual(round_rational(Fraction(7, 2), RoundingPolicy.HALF_EVEN), 4)

    def test_negative_rounding_half_even(self):
        self.assertEqual(round_rational(Fraction(-1, 2), RoundingPolicy.HALF_EVEN), 0)
        self.assertEqual(round_rational(Fraction(-3, 2), RoundingPolicy.HALF_EVEN), -2)
        self.assertEqual(round_rational(Fraction(-5, 2), RoundingPolicy.HALF_EVEN), -2)
        self.assertEqual(round_rational(Fraction(-1, 3), RoundingPolicy.HALF_EVEN), 0)
        self.assertEqual(round_rational(Fraction(-2, 3), RoundingPolicy.HALF_EVEN), -1)

    def test_negative_rounding_other_policies(self):
        neg = Fraction(-3, 2)
        self.assertEqual(round_rational(neg, RoundingPolicy.FLOOR), -2)
        self.assertEqual(round_rational(neg, RoundingPolicy.CEIL), -1)
        self.assertEqual(round_rational(neg, RoundingPolicy.TRUNC), -1)
        self.assertEqual(round_rational(neg, RoundingPolicy.HALF_AWAY), -2)
        self.assertEqual(round_rational(Fraction(3, 2), RoundingPolicy.HALF_AWAY), 2)
        self.assertEqual(round_rational(neg, RoundingPolicy.HALF_UP), -1)


class TestQuantities(unittest.TestCase):
    def test_units_unified(self):
        self.assertEqual(Duration.of(1, MILLISECOND).to(MICROSECOND), 1000)
        tick = Duration.of(1, SAMPLE_48K)
        self.assertEqual(tick.to(MICROSECOND), Fraction(125, 6))
        self.assertEqual(tick.to_int(MICROSECOND), 21)  # 20.8333... -> 21

    def test_timepoint_arithmetic(self):
        t0 = TimePoint.of("mono", 10, SECOND)
        t1 = t0 + Duration.of(1500, MILLISECOND)
        self.assertEqual(t1.to(SECOND), Fraction(23, 2))
        self.assertEqual(t1 - t0, Duration.of(3, SECOND) / 2)
        with self.assertRaises(TypeError):
            _ = t0 + t1
        with self.assertRaises(ValueError):
            _ = t1 - TimePoint.of("other", 0, SECOND)
        with self.assertRaises(ValueError):
            _ = t0 < TimePoint.of("other", 1, SECOND)

    def test_wall_clock_distinct_and_exact(self):
        w = WallClockTime.from_datetime(
            datetime.datetime(2026, 10, 1, 12, 0, 0, tzinfo=datetime.timezone.utc),
            nanosecond=7,
        )
        self.assertEqual(w.to(NANOSECOND) % 1_000_000_000, 7)
        later = w + Duration.of(1, SAMPLE_48K)
        self.assertEqual((later - w).seconds, Fraction(1, 48_000))

    def test_large_epoch_plus_tiny_interval_no_precision_loss(self):
        big = WallClockTime.of(1_700_000_000, SECOND)
        later = big + Duration.of(1, SAMPLE_48K)
        self.assertEqual(
            later.seconds_since_epoch,
            Fraction(1_700_000_000) + Fraction(1, 48_000),
        )
        # 2**60 ns apart by 1 ns: float64 cannot tell these apart.
        a = TimePoint.of("hw", 2**60, NANOSECOND)
        b = a + Duration.of(1, NANOSECOND)
        self.assertLess(a, b)
        self.assertEqual(b - a, Duration.of(1, NANOSECOND))
        self.assertEqual(float(a.to(NANOSECOND)), float(b.to(NANOSECOND)))


class TestCalibration(unittest.TestCase):
    def test_exact_map_and_inverse(self):
        cal = AffineCalibration("A", "B", Fraction(48_000, 1), Fraction(3))
        self.assertEqual(cal.map_value(Fraction(1, 48_000)), 4)
        inv = cal.inverse()
        self.assertEqual(inv.map_value(cal.map_value(Fraction(7, 3))), Fraction(7, 3))

    def test_error_budget_correlation_roundtrip_cancels(self):
        cal = AffineCalibration(
            "A", "B", Fraction(2), Fraction(1), error=Fraction(1, 10)
        )
        out = cal.map_budget(ErrorBudget.exact(5))
        self.assertEqual(out.half_width, Fraction(1, 10))
        back = cal.inverse().map_budget(out)
        self.assertEqual(back.center, 5)
        self.assertEqual(back.half_width, 0)  # same source: correlated, cancels

    def test_independent_errors_accumulate(self):
        c1 = AffineCalibration("A", "B", Fraction(1), Fraction(0), Fraction(1, 10))
        c2 = AffineCalibration("B", "C", Fraction(1), Fraction(0), Fraction(1, 10))
        out = c2.map_budget(c1.map_budget(ErrorBudget.exact(0)))
        self.assertEqual(out.half_width, Fraction(2, 10))

    def test_preimage_is_interval_multiple_solutions(self):
        cal = AffineCalibration(
            "A", "B", Fraction(2), Fraction(0), error=Fraction(1, 5)
        )
        lo, hi = cal.preimage(Fraction(10), Fraction(10))
        self.assertEqual((lo, hi), (Fraction(49, 10), Fraction(51, 10)))
        self.assertLess(lo, hi)  # a whole interval of solutions


def _triangle_graph(offset_c_to_a=Fraction(0)):
    g = CalibrationGraph()
    g.add_calibration(AffineCalibration("A", "B", Fraction(1), Fraction(5)))
    g.add_calibration(AffineCalibration("B", "C", Fraction(2), Fraction(-3)))
    g.add_calibration(AffineCalibration("C", "A", Fraction(1, 2), offset_c_to_a))
    return g


class TestGraph(unittest.TestCase):
    def test_convert_matches_direct_and_multipath(self):
        g = CalibrationGraph()
        g.add_calibration(AffineCalibration("A", "B", Fraction(2), Fraction(1)))
        g.add_calibration(AffineCalibration("B", "C", Fraction(3), Fraction(-1)))
        g.add_calibration(AffineCalibration("A", "C", Fraction(6), Fraction(2)))
        self.assertEqual(g.convert(Fraction(4), "A", "C"), 26)
        paths = g.compare_paths("A", "C")
        self.assertEqual(len(paths), 2)
        for _, cal in paths:
            self.assertEqual(cal.map_value(Fraction(4)), 26)

    def test_cycle_audit_consistent(self):
        # C->A chosen so the loop composes to the identity.
        g = _triangle_graph(offset_c_to_a=Fraction(-7, 2))
        reports = g.audit()
        self.assertTrue(reports)
        self.assertTrue(all(r.consistent for r in reports))

    def test_cycle_audit_contradiction_evidence(self):
        g = _triangle_graph(offset_c_to_a=Fraction(0))
        reports = [r for r in g.audit() if not r.consistent]
        self.assertEqual(len(reports), 1)
        report = reports[0]
        self.assertEqual(report.residual_slope, 0)
        self.assertEqual(report.residual_offset, Fraction(-7, 2))
        self.assertIn("CONTRADICTION", report.describe())
        self.assertEqual(report.clocks[0], report.clocks[-1])

    def test_budget_propagation_through_graph(self):
        g = CalibrationGraph()
        g.add_calibration(
            AffineCalibration("A", "B", Fraction(2), Fraction(0), Fraction(1, 10))
        )
        g.add_calibration(
            AffineCalibration("B", "C", Fraction(3), Fraction(0), Fraction(1, 10))
        )
        out = g.convert_budget(ErrorBudget.exact(1), "A", "C")
        self.assertEqual(out.center, 6)
        # edge1 error scaled by slope 3, plus edge2 error: 3/10 + 1/10
        self.assertEqual(out.half_width, Fraction(4, 10))
        back = g.convert_budget(out, "C", "A")
        self.assertEqual(back.half_width, 0)  # correlated round trip

    def test_local_correction_invalidates_only_dependent_cache(self):
        g = CalibrationGraph()
        g.add_calibration(AffineCalibration("A", "B", Fraction(1), Fraction(1)))
        g.add_calibration(AffineCalibration("B", "C", Fraction(1), Fraction(1)))
        g.add_calibration(AffineCalibration("C", "D", Fraction(1), Fraction(1)))
        self.assertEqual(g.convert(0, "A", "C"), 2)
        self.assertEqual(g.convert(0, "C", "D"), 1)
        g.add_calibration(AffineCalibration("A", "B", Fraction(1), Fraction(10)))
        history_ac = g.cache_history("A", "C")
        self.assertEqual(len(history_ac), 1)
        self.assertFalse(history_ac[0].valid)  # dependent entry stale
        history_cd = g.cache_history("C", "D")
        self.assertTrue(history_cd[0].valid)   # unrelated entry untouched
        self.assertEqual(g.convert(0, "A", "C"), 11)
        history_ac = g.cache_history("A", "C")
        self.assertEqual(len(history_ac), 2)   # old version preserved
        self.assertEqual(history_ac[0].calibration.offset, 2)
        self.assertEqual(history_ac[1].calibration.offset, 11)


class TestConversionTable(unittest.TestCase):
    def test_table_matches_direct_exact_conversion(self):
        table = ConversionTable.build(SAMPLE_48K, MICROSECOND, stride=480, count=20)
        self.assertTrue(table.verify())
        self.assertEqual(table.offset_per_stride, Fraction(10_000))
        for tick in range(0, 480 * 20, 37):
            exact = Fraction(tick, 48_000) * 1_000_000
            self.assertEqual(
                table.convert(tick),
                round_rational(exact, RoundingPolicy.HALF_EVEN),
            )

    def test_table_out_of_range_falls_back_exact(self):
        table = ConversionTable.build(SAMPLE_48K, MICROSECOND, stride=48, count=2)
        self.assertEqual(
            table.convert(10**9),
            round_rational(Fraction(10**9, 48_000) * 1_000_000),
        )


if __name__ == "__main__":
    unittest.main()
