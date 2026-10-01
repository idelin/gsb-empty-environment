import unittest
from datetime import date
from fractions import Fraction

from calpart.calendars import DayCalendar, MonthCalendar, WeekCalendar
from calpart.exact import EPOCH_DATE, Interval
from calpart.partition import (
    WorkCounter,
    partition_window,
    reference_verify,
    verify_coverage,
)
from tests.helpers import FALLBACK, REPEAT_DAY, SKIP_DAY, UTC, utc


def durations_by_label(segments, key="day"):
    return {seg.labels[key]: seg.duration.seconds for seg in segments}


class TestFallbackDay(unittest.TestCase):
    def test_25_hour_day_and_exact_coverage(self):
        window = Interval(utc(2023, 10, 28), utc(2023, 10, 31))
        segments = partition_window(FALLBACK, window, [DayCalendar()])
        got = durations_by_label(segments)
        self.assertEqual(
            got,
            {
                "2023-10-28": Fraction(22 * 3600),
                "2023-10-29": Fraction(25 * 3600),  # offset rollback day
                "2023-10-30": Fraction(24 * 3600),
                "2023-10-31": Fraction(1 * 3600),
            },
        )
        report = verify_coverage(segments, window)
        self.assertTrue(report.ok, report.problems)
        self.assertEqual(report.total_duration.seconds, window.length)
        self.assertTrue(reference_verify(FALLBACK, window, segments))

    def test_fallback_day_intervals_are_exact(self):
        window = Interval(utc(2023, 10, 28), utc(2023, 10, 31))
        segments = partition_window(FALLBACK, window, [DayCalendar()])
        day29 = next(s for s in segments if s.labels["day"] == "2023-10-29")
        self.assertEqual(len(day29.intervals), 1)
        iv = day29.intervals[0]
        self.assertEqual((iv.start, iv.end), (utc(2023, 10, 28, 22), utc(2023, 10, 29, 23)))


class TestSkippedDay(unittest.TestCase):
    def test_skipped_date_absent_but_coverage_conserved(self):
        window = Interval(utc(2011, 12, 28), utc(2012, 1, 2))
        segments = partition_window(SKIP_DAY, window, [DayCalendar()])
        labels = [seg.labels["day"] for seg in segments]
        self.assertNotIn("2011-12-30", labels)
        self.assertIn("2011-12-29", labels)
        self.assertIn("2011-12-31", labels)
        got = durations_by_label(segments)
        self.assertEqual(got["2011-12-29"], Fraction(24 * 3600))
        self.assertEqual(got["2011-12-31"], Fraction(24 * 3600))
        report = verify_coverage(segments, window)
        self.assertTrue(report.ok, report.problems)
        self.assertTrue(reference_verify(SKIP_DAY, window, segments))


class TestRepeatedDay(unittest.TestCase):
    def test_day_with_two_disjoint_preimages(self):
        window = Interval(utc(2020, 6, 13), utc(2020, 6, 17))
        segments = partition_window(REPEAT_DAY, window, [DayCalendar()])
        got = durations_by_label(segments)
        self.assertEqual(
            got,
            {
                "2020-06-13": Fraction(30 * 3600),
                "2020-06-14": Fraction(48 * 3600),
                "2020-06-15": Fraction(18 * 3600),
            },
        )
        day14 = next(s for s in segments if s.labels["day"] == "2020-06-14")
        self.assertEqual(len(day14.intervals), 2)
        first, second = day14.intervals
        self.assertEqual((first.start, first.end), (utc(2020, 6, 14, 0), utc(2020, 6, 15, 0)))
        self.assertEqual((second.start, second.end), (utc(2020, 6, 15, 6), utc(2020, 6, 16, 6)))
        self.assertLess(first.end, second.start)  # genuinely disjoint
        report = verify_coverage(segments, window)
        self.assertTrue(report.ok, report.problems)
        self.assertTrue(reference_verify(REPEAT_DAY, window, segments))


class TestMonthAcrossYear(unittest.TestCase):
    def test_year_boundary_not_missed(self):
        window = Interval(utc(2023, 12, 15), utc(2024, 2, 15))
        segments = partition_window(UTC, window, [MonthCalendar()])
        got = durations_by_label(segments, "month")
        self.assertEqual(
            got,
            {
                "2023-12": Fraction(17 * 86400),
                "2024-01": Fraction(31 * 86400),
                "2024-02": Fraction(14 * 86400),
            },
        )
        report = verify_coverage(segments, window)
        self.assertTrue(report.ok, report.problems)
        self.assertTrue(reference_verify(UTC, window, segments))


class TestCommonRefinement(unittest.TestCase):
    def test_day_week_month_refinement(self):
        window = Interval(utc(2023, 10, 27), utc(2023, 11, 2))
        calendars = [DayCalendar(), WeekCalendar(6), MonthCalendar()]
        segments = partition_window(FALLBACK, window, calendars)
        for seg in segments:
            self.assertEqual(set(seg.labels), {"day", "week(start=6)", "month"})
        labels = {(s.labels["day"], s.labels["week(start=6)"], s.labels["month"]) for s in segments}
        self.assertIn(("2023-10-29", "2023-10-29", "2023-10"), labels)
        self.assertIn(("2023-11-01", "2023-10-29", "2023-11"), labels)
        report = verify_coverage(segments, window)
        self.assertTrue(report.ok, report.problems)
        self.assertTrue(reference_verify(FALLBACK, window, segments))

    def test_two_week_starts_refinement(self):
        window = Interval(utc(2023, 10, 25), utc(2023, 11, 2))
        calendars = [WeekCalendar(0), WeekCalendar(6)]
        segments = partition_window(UTC, window, calendars)
        keys = {(s.labels["week(start=0)"], s.labels["week(start=6)"]) for s in segments}
        self.assertIn(("2023-10-23", "2023-10-22"), keys)
        self.assertIn(("2023-10-30", "2023-10-29"), keys)
        # Both Sunday and Monday midnights are cut points inside the window.
        cuts = {iv.start for s in segments for iv in s.intervals} | {
            iv.end for s in segments for iv in s.intervals
        }
        self.assertIn(utc(2023, 10, 29), cuts)
        self.assertIn(utc(2023, 10, 30), cuts)
        report = verify_coverage(segments, window)
        self.assertTrue(report.ok, report.problems)


class TestExactEndpoints(unittest.TestCase):
    def test_fractional_window_edges(self):
        start = Fraction(1, 7)
        window = Interval(start, start + 3 * 86400)
        segments = partition_window(UTC, window, [DayCalendar()])
        got = durations_by_label(segments)
        self.assertEqual(
            sorted(got.values()),
            [Fraction(1, 7), 86400 - Fraction(1, 7), Fraction(86400), Fraction(86400)],
        )
        for seg in segments:
            self.assertIsInstance(seg.duration.seconds, Fraction)
        report = verify_coverage(segments, window)
        self.assertTrue(report.ok, report.problems)

    def test_empty_window(self):
        window = Interval(100, 100)
        self.assertEqual(partition_window(UTC, window, [DayCalendar()]), [])


class TestLongWindowJumps(unittest.TestCase):
    def test_5000_years_by_months_jump_based(self):
        days = (date(6970, 1, 1) - EPOCH_DATE).days
        window = Interval(0, days * 86400)
        work = WorkCounter()
        segments = partition_window(UTC, window, [MonthCalendar()], work)
        self.assertEqual(len(segments), 5000 * 12)
        # Effort is proportional to the number of boundaries, not seconds.
        self.assertLessEqual(work.boundary_jumps, 5000 * 12 + 4)
        self.assertGreater(window.length, 10**11)
        self.assertLess(work.total() * 1_000_000, window.length)
        report = verify_coverage(segments, window)
        self.assertTrue(report.ok, report.problems)


if __name__ == "__main__":
    unittest.main()
