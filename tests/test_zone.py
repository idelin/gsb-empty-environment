import datetime as dt
import unittest
from fractions import Fraction

from chronodur import (Duration, Instant, WallTime, WallTimeError,
                       parse_duration)

from tests.helpers import us_style_zone, utc_seconds


class TestInstantWallRoundTrip(unittest.TestCase):
    def setUp(self):
        self.zone = us_style_zone()

    def test_around_every_transition(self):
        for tr in self.zone.transitions:
            for delta in (-2, -1, 0, 1, 2, 3599, 3600, 3601):
                t = Fraction(tr.utc_seconds + delta)
                with self.subTest(t=t):
                    wall = Instant(t).to_wall(self.zone)
                    back = wall.to_instant().seconds
                    if back != t:
                        # Only legal inside a fold: the wall reading is
                        # ambiguous and resolves to its earlier occurrence.
                        self.assertEqual(back, t - 3600)
                        self.assertNotEqual(
                            self.zone.offset_at(back), self.zone.offset_at(t))

    def test_unambiguous_instants_round_trip(self):
        # Sweep two years in 6-hour steps: every non-fold instant round-trips.
        start = utc_seconds(2024, 1, 1)
        for k in range(4 * 365 * 2):
            t = Fraction(start + k * 6 * 3600)
            wall = Instant(t).to_wall(self.zone)
            back = wall.to_instant().seconds
            if back != t:
                self.assertEqual(back, t - 3600)  # fold second occurrence

    def test_subsecond_exactness(self):
        t = Fraction(utc_seconds(2024, 6, 1, 12)) + Fraction(1, 3)
        wall = Instant(t).to_wall(self.zone)
        self.assertEqual(wall.sub, Fraction(1, 3))
        self.assertEqual(wall.to_instant().seconds, t)

    def test_wall_gap_rejected(self):
        wall = WallTime(dt.datetime(2024, 3, 10, 2, 30), self.zone)
        with self.assertRaises(WallTimeError):
            wall.to_instant()

    def test_wall_fold_resolves_to_earlier_instant(self):
        wall = WallTime(dt.datetime(2024, 11, 3, 1, 30), self.zone)
        self.assertEqual(wall.to_instant().seconds,
                         Fraction(utc_seconds(2024, 11, 3, 5, 30)))


class TestTypeDistinctions(unittest.TestCase):
    def setUp(self):
        self.zone = us_style_zone()

    def test_instant_minus_instant_is_duration(self):
        a = Instant(Fraction(1000))
        b = Instant(Fraction(1600))
        delta = b - a
        self.assertIsInstance(delta, Duration)
        self.assertEqual(delta, Duration(fixed=Fraction(600)))
        self.assertEqual(delta.months, 0)
        self.assertEqual(delta.cal_days, 0)

    def test_wall_time_is_not_an_instant(self):
        wall = WallTime(dt.datetime(2024, 6, 1, 12, 0), self.zone)
        self.assertNotIsInstance(wall, Instant)
        self.assertIsInstance(wall.to_instant(), Instant)

    def test_duration_is_not_an_instant(self):
        self.assertNotIsInstance(parse_duration("1d"), Instant)
        self.assertNotIsInstance(parse_duration("1d"), WallTime)


if __name__ == "__main__":
    unittest.main()
