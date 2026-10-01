import unittest
from datetime import date

from calpart.calendars import DayCalendar, MonthCalendar, WeekCalendar, iter_boundaries
from calpart.exact import WallTime


class TestCalendars(unittest.TestCase):
    def test_day_floor_and_boundary(self):
        cal = DayCalendar()
        w = WallTime(date(2023, 10, 29), 43200)
        self.assertEqual(cal.floor(w), WallTime(date(2023, 10, 29), 0))
        self.assertEqual(cal.next_boundary(w), WallTime(date(2023, 10, 30), 0))
        self.assertEqual(cal.next_boundary(cal.floor(w)), WallTime(date(2023, 10, 30), 0))
        self.assertEqual(cal.label(w), "2023-10-29")

    def test_week_custom_start(self):
        monday = WeekCalendar(0)
        sunday = WeekCalendar(6)
        w = WallTime(date(2023, 10, 29), 3600)  # a Sunday
        self.assertEqual(monday.floor(w).day, date(2023, 10, 23))
        self.assertEqual(sunday.floor(w).day, date(2023, 10, 29))
        self.assertEqual(sunday.next_boundary(w), WallTime(date(2023, 11, 5), 0))
        self.assertEqual(monday.label(w), "2023-10-23")
        self.assertEqual(sunday.label(w), "2023-10-29")

    def test_week_start_validation(self):
        with self.assertRaises(ValueError):
            WeekCalendar(7)

    def test_month_crosses_year(self):
        cal = MonthCalendar()
        w = WallTime(date(2023, 12, 31), 23 * 3600)
        self.assertEqual(cal.floor(w), WallTime(date(2023, 12, 1), 0))
        self.assertEqual(cal.next_boundary(w), WallTime(date(2024, 1, 1), 0))
        self.assertEqual(cal.label(w), "2023-12")
        w2 = WallTime(date(2024, 1, 1), 0)
        self.assertEqual(cal.next_boundary(w2), WallTime(date(2024, 2, 1), 0))

    def test_iter_boundaries_counts_jumps(self):
        class Work:
            boundary_jumps = 0

        work = Work()
        days = list(
            iter_boundaries(
                DayCalendar(),
                WallTime(date(2024, 1, 1), 0),
                WallTime(date(2024, 1, 10), 0),
                work,
            )
        )
        self.assertEqual(len(days), 9)
        self.assertEqual(work.boundary_jumps, 9)
        self.assertEqual(days[0], WallTime(date(2024, 1, 2), 0))
        self.assertEqual(days[-1], WallTime(date(2024, 1, 10), 0))


if __name__ == "__main__":
    unittest.main()
