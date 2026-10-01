import unittest

from calpart import DayGrid, MonthGrid, WeekGrid
from tests.helpers import U


class TestDayGrid(unittest.TestCase):
    def test_floor_and_next(self):
        grid = DayGrid()
        self.assertEqual(grid.floor(U(2024, 10, 27, 13, 45)), U(2024, 10, 27))
        self.assertEqual(grid.next_boundary(U(2024, 10, 27, 13, 45)), U(2024, 10, 28))
        self.assertEqual(grid.next_boundary(U(2024, 10, 27)), U(2024, 10, 28))

    def test_label(self):
        self.assertEqual(DayGrid().label(U(2024, 10, 27)), "2024-10-27")

    def test_count_between(self):
        self.assertEqual(DayGrid().count_between(U(2024, 1, 1), U(2025, 1, 1)), 366)


class TestWeekGrid(unittest.TestCase):
    def test_custom_week_start_changes_floor(self):
        monday = WeekGrid(0)
        sunday = WeekGrid(6)
        probe = U(2024, 10, 27, 12)  # 2024-10-27 is a Sunday
        self.assertEqual(monday.floor(probe), U(2024, 10, 21))
        self.assertEqual(sunday.floor(probe), U(2024, 10, 27))

    def test_week_start_boundary_detection(self):
        sunday = WeekGrid(6)
        self.assertTrue(sunday.is_boundary(U(2024, 10, 27)))
        self.assertFalse(sunday.is_boundary(U(2024, 10, 28)))

    def test_advance_and_count(self):
        grid = WeekGrid(2)  # Wednesday weeks
        start = grid.floor(U(2024, 1, 10))
        self.assertEqual(grid.advance(start, 4), start + 4 * 7 * 86400)
        self.assertEqual(grid.count_between(start, grid.advance(start, 4)), 4)

    def test_invalid_week_start(self):
        with self.assertRaises(ValueError):
            WeekGrid(7)


class TestMonthGrid(unittest.TestCase):
    def test_next_boundary_crosses_year(self):
        grid = MonthGrid()
        self.assertEqual(grid.next_boundary(U(2024, 12, 1)), U(2025, 1, 1))
        self.assertEqual(grid.next_boundary(U(2024, 12, 15)), U(2025, 1, 1))

    def test_floor(self):
        grid = MonthGrid()
        self.assertEqual(grid.floor(U(2024, 12, 31, 23, 59)), U(2024, 12, 1))

    def test_advance_across_years(self):
        grid = MonthGrid()
        self.assertEqual(grid.advance(U(2024, 11, 1), 3), U(2025, 2, 1))
        self.assertEqual(grid.count_between(U(2024, 11, 1), U(2025, 2, 1)), 3)

    def test_label(self):
        self.assertEqual(MonthGrid().label(U(2025, 1, 1)), "2025-01")

    def test_leap_year_february(self):
        grid = MonthGrid()
        feb = U(2024, 2, 1)
        self.assertEqual(grid.next_boundary(feb) - feb, 29 * 86400)
        feb = U(2025, 2, 1)
        self.assertEqual(grid.next_boundary(feb) - feb, 28 * 86400)


if __name__ == "__main__":
    unittest.main()
