import datetime as dt
import unittest
from fractions import Fraction

from chronodur import (Duration, Instant, WallTime, WallTimeError, add_months,
                       compile_program, parse_duration)

from tests.helpers import us_style_zone, utc_seconds


class TestMonthEndClamping(unittest.TestCase):
    def test_clamp_to_short_month(self):
        self.assertEqual(add_months(dt.date(2024, 1, 31), 1), dt.date(2024, 2, 29))
        self.assertEqual(add_months(dt.date(2023, 1, 31), 1), dt.date(2023, 2, 28))

    def test_leap_day_plus_year(self):
        self.assertEqual(add_months(dt.date(2024, 2, 29), 12), dt.date(2025, 2, 28))

    def test_negative_months(self):
        self.assertEqual(add_months(dt.date(2024, 3, 31), -1), dt.date(2024, 2, 29))

    def test_clamping_is_not_reversible(self):
        # 2024-01-31 +1mo -> 2024-02-29, +1mo again -> 2024-03-29 (not 31).
        once = add_months(dt.date(2024, 1, 31), 1)
        self.assertEqual(add_months(once, 1), dt.date(2024, 3, 29))


class TestCalendarDayVsFixedDay(unittest.TestCase):
    def setUp(self):
        self.zone = us_style_zone()

    def test_calendar_day_across_spring_forward_is_23_hours(self):
        base = WallTime(dt.datetime(2024, 3, 9, 12, 0), self.zone)
        plus_cd = compile_program("1cd").apply(base)
        plus_d = compile_program("1d").apply(base)
        self.assertEqual(plus_cd.dt, dt.datetime(2024, 3, 10, 12, 0))
        self.assertEqual(plus_d.dt, dt.datetime(2024, 3, 10, 13, 0))
        delta_cd = (plus_cd.to_instant() - base.to_instant()).fixed
        delta_d = (plus_d.to_instant() - base.to_instant()).fixed
        self.assertEqual(delta_cd, Fraction(23 * 3600))  # 1cd != 24h here
        self.assertEqual(delta_d, Fraction(86400))

    def test_calendar_day_across_fall_back_is_25_hours(self):
        base = WallTime(dt.datetime(2024, 11, 2, 12, 0), self.zone)
        plus = compile_program("1cd").apply(base)
        delta = (plus.to_instant() - base.to_instant()).fixed
        self.assertEqual(delta, Fraction(25 * 3600))

    def test_fixed_day_is_always_86400_seconds(self):
        base = WallTime(dt.datetime(2024, 3, 9, 12, 0), self.zone)
        plus = compile_program("1d").apply(base)
        self.assertEqual((plus.to_instant() - base.to_instant()).fixed,
                         Fraction(86400))


class TestNonCommutation(unittest.TestCase):
    def test_month_and_fixed_seconds_counterexample(self):
        zone = us_style_zone()
        base = WallTime(dt.datetime(2024, 3, 9, 12, 0), zone)
        month_then_day = compile_program("1mo 1d").apply(base)
        day_then_month = compile_program("1d 1mo").apply(base)
        self.assertEqual(month_then_day.dt, dt.datetime(2024, 4, 10, 12, 0))
        self.assertEqual(day_then_month.dt, dt.datetime(2024, 4, 10, 13, 0))
        self.assertNotEqual(month_then_day, day_then_month)

    def test_values_agree_but_programs_differ(self):
        # As values the two expressions are identical; as ordered programs
        # they are not, and normalization never reorders a program.
        self.assertEqual(parse_duration("1mo 1d"), parse_duration("1d 1mo"))
        self.assertNotEqual(compile_program("1mo 1d").ops,
                            compile_program("1d 1mo").ops)


class TestProgramComposition(unittest.TestCase):
    def test_apply_is_sequential(self):
        zone = us_style_zone()
        base = WallTime(dt.datetime(2024, 1, 31, 9, 30), zone)
        p1, p2 = compile_program("1mo"), compile_program("1cd")
        self.assertEqual(p1.then(p2).apply(base), p2.apply(p1.apply(base)))

    def test_month_end_through_program(self):
        zone = us_style_zone()
        base = WallTime(dt.datetime(2024, 1, 31, 9, 30), zone)
        result = compile_program("1mo").apply(base)
        self.assertEqual(result.dt, dt.datetime(2024, 2, 29, 9, 30))


class TestGapAndFold(unittest.TestCase):
    def test_gap_rejected(self):
        zone = us_style_zone()
        wall = WallTime(dt.datetime(2024, 3, 10, 2, 30), zone)
        with self.assertRaises(WallTimeError):
            wall.to_instant()

    def test_fold_picks_earlier_instant(self):
        zone = us_style_zone()
        wall = WallTime(dt.datetime(2024, 11, 3, 1, 30), zone)
        self.assertEqual(wall.to_instant().seconds,
                         Fraction(utc_seconds(2024, 11, 3, 5, 30)))


class TestExactSubSecond(unittest.TestCase):
    def test_rational_seconds_survive_application(self):
        zone = us_style_zone()
        base = WallTime(dt.datetime(2024, 6, 1, 12, 0), zone, Fraction(1, 3))
        result = compile_program("1/2s").apply(base)
        self.assertEqual(result.sub, Fraction(5, 6))
        delta = result.to_instant() - base.to_instant()
        self.assertEqual(delta, Duration(fixed=Fraction(1, 2)))


if __name__ == "__main__":
    unittest.main()
