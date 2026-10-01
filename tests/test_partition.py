import unittest
from fractions import Fraction

from calpart import (
    DayGrid,
    MonthGrid,
    OffsetTable,
    SegmentPager,
    WeekGrid,
    iter_segments,
    partition,
    verify_segments_cover,
)
from tests.helpers import U, europe_like, wild_jump_table


class TestDayPartition(unittest.TestCase):
    def test_fall_back_day_is_25_hours(self):
        window = (U(2024, 10, 26), U(2024, 10, 29))
        segs = partition(window, europe_like(), DayGrid())
        self.assertEqual(
            [s.label for s in segs],
            ["2024-10-26", "2024-10-27", "2024-10-28", "2024-10-29"],
        )
        self.assertEqual([s.duration for s in segs], [79200, 90000, 86400, 3600])
        by_label = {s.label: s for s in segs}
        self.assertEqual(by_label["2024-10-27"].start, U(2024, 10, 26, 22))
        self.assertEqual(by_label["2024-10-27"].end, U(2024, 10, 27, 23))
        verify_segments_cover(segs, window)

    def test_spring_forward_day_is_23_hours(self):
        window = (U(2024, 3, 30), U(2024, 4, 2))
        segs = partition(window, europe_like(), DayGrid())
        by_label = {s.label: s for s in segs}
        self.assertEqual(by_label["2024-03-31"].duration, 23 * 3600)
        verify_segments_cover(segs, window)

    def test_disjoint_preimages_of_one_local_day(self):
        window = (U(2024, 1, 1), U(2024, 1, 5))
        segs = partition(window, wild_jump_table(), DayGrid())
        jan2 = [s for s in segs if s.label == "2024-01-02"]
        self.assertEqual(len(jan2), 2)
        self.assertEqual(
            [(s.start, s.end) for s in jan2],
            [(U(2024, 1, 2), U(2024, 1, 2, 12)), (U(2024, 1, 3, 6), U(2024, 1, 4))],
        )
        self.assertEqual(sum(s.duration for s in jan2), 30 * 3600)
        verify_segments_cover(segs, window)

    def test_exact_fractional_window_endpoints(self):
        start = Fraction(1, 3)
        end = Fraction(1, 3) + 2 * 86400 + Fraction(1, 7)
        window = (start, end)
        segs = partition(window, OffsetTable.fixed(0), DayGrid())
        self.assertEqual(segs[0].start, start)
        self.assertEqual(segs[-1].end, end)
        verify_segments_cover(segs, window)


class TestWeekPartition(unittest.TestCase):
    def test_custom_week_start_moves_cut_points(self):
        window = (U(2024, 10, 26, 12), U(2024, 10, 30, 12))
        table = OffsetTable.fixed(0)
        sunday = partition(window, table, WeekGrid(6))
        self.assertEqual([s.label for s in sunday],
                         ["week[Sun]@2024-10-20", "week[Sun]@2024-10-27"])
        self.assertEqual(sunday[0].end, U(2024, 10, 27))
        monday = partition(window, table, WeekGrid(0))
        self.assertEqual(monday[0].end, U(2024, 10, 28))
        verify_segments_cover(sunday, window)
        verify_segments_cover(monday, window)


class TestMonthPartition(unittest.TestCase):
    def test_month_end_crossing_year_drops_no_period(self):
        window = (U(2024, 12, 15), U(2025, 2, 10))
        segs = partition(window, OffsetTable.fixed(0), MonthGrid())
        self.assertEqual([s.label for s in segs], ["2024-12", "2025-01", "2025-02"])
        january = next(s for s in segs if s.label == "2025-01")
        self.assertEqual(january.duration, 31 * 86400)
        self.assertEqual(january.start, U(2025, 1, 1))
        self.assertEqual(january.end, U(2025, 2, 1))
        verify_segments_cover(segs, window)


class TestPagination(unittest.TestCase):
    def test_pages_reconstruct_full_coverage(self):
        window = (U(2024, 5, 1), U(2024, 5, 11))
        table = OffsetTable.fixed(0)
        pager = SegmentPager(iter_segments(window, table, DayGrid()), page_size=4)
        pages = list(pager)
        self.assertEqual([len(p) for p in pages], [4, 4, 2])
        flat = [s for page in pages for s in page]
        verify_segments_cover(flat, window)

    def test_pagination_is_lazy(self):
        window = (U(2024, 1, 1), U(2024, 2, 1))
        table = OffsetTable.fixed(0)
        pager = SegmentPager(iter_segments(window, table, DayGrid()), page_size=5)
        first = pager.next_page()
        self.assertEqual(len(first), 5)
        self.assertEqual(pager.pages_served, 1)
        self.assertFalse(pager.exhausted)
        rest = []
        while not pager.exhausted:
            rest.extend(pager.next_page())
        self.assertEqual(len(first) + len(rest), 31)
        self.assertEqual(pager.next_page(), [])


if __name__ == "__main__":
    unittest.main()
