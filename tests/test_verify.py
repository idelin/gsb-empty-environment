import unittest
from fractions import Fraction

from calpart import (
    DayGrid,
    MonthGrid,
    OffsetTable,
    Segment,
    WeekGrid,
    WorkStats,
    jump_scan,
    partition,
    verify_piecewise_conservation,
    verify_segments_cover,
)
from tests.helpers import U, europe_like


class TestPiecewiseConservation(unittest.TestCase):
    def test_transition_cutting_conserves_coverage(self):
        window = (U(2024, 1, 1), U(2025, 1, 1))
        stats = WorkStats()
        total, _ = verify_piecewise_conservation(window, europe_like(), stats)
        self.assertEqual(total, window[1] - window[0])
        self.assertEqual(stats.transitions_scanned, 2)
        self.assertEqual(stats.pieces_checked, 3)

    def test_partition_segments_cover_window(self):
        window = (U(2024, 3, 1), U(2024, 11, 1))
        segs = partition(window, europe_like(), DayGrid())
        total = verify_segments_cover(segs, window)
        self.assertEqual(total, window[1] - window[0])

    def test_cover_check_detects_gap(self):
        window = (0, 100)
        bad = [Segment(0, 40, "a"), Segment(50, 100, "b")]
        with self.assertRaises(AssertionError):
            verify_segments_cover(bad, window)

    def test_cover_check_detects_overlap(self):
        window = (0, 100)
        bad = [Segment(0, 60, "a"), Segment(50, 100, "b")]
        with self.assertRaises(AssertionError):
            verify_segments_cover(bad, window)


class TestJumpScan(unittest.TestCase):
    def test_enormous_window_is_verified_by_jumping(self):
        start = U(2024, 1, 1)
        window = (start, start + 36_524_250 * 86400)  # 100 000 Gregorian years
        stats = WorkStats()
        total, _ = jump_scan(window, europe_like(), DayGrid(), stats)
        self.assertEqual(total, window[1] - window[0])
        self.assertGreater(stats.periods_jumped, 36_000_000)
        self.assertLess(stats.bulk_jumps, 10)
        self.assertEqual(stats.transitions_scanned, 4)

    def test_jump_scan_matches_naive_period_count(self):
        window = (U(2024, 1, 1), U(2025, 1, 1))
        stats = WorkStats()
        jump_scan(window, europe_like(), DayGrid(), stats)
        naive = len(partition(window, europe_like(), DayGrid()))
        self.assertGreaterEqual(stats.periods_jumped, naive)
        self.assertLessEqual(stats.periods_jumped, naive + stats.transitions_scanned)

    def test_jump_scan_with_month_and_week_grids(self):
        start = U(2024, 1, 1)
        window = (start, start + 3_652_425 * 86400)  # 10 000 Gregorian years
        stats = WorkStats()
        total, _ = jump_scan(window, europe_like(), MonthGrid(), stats)
        self.assertEqual(total, window[1] - window[0])
        self.assertGreaterEqual(stats.periods_jumped, 120_001)
        self.assertLessEqual(stats.periods_jumped, 120_001 + stats.transitions_scanned)
        stats = WorkStats()
        jump_scan(window, europe_like(), WeekGrid(6), stats)
        self.assertGreater(stats.periods_jumped, 500_000)


if __name__ == "__main__":
    unittest.main()
