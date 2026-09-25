import datetime as dt
import unittest

from recur import Duration, Instant, WallTime


class TimeModelTest(unittest.TestCase):
    def test_wall_parts_and_round_trip(self):
        w = WallTime.from_parts(2024, 3, 31, 7, 5, 9, 123_456)
        self.assertEqual((w.year, w.month, w.day), (2024, 3, 31))
        self.assertEqual((w.hour, w.minute, w.second, w.micros), (7, 5, 9, 123_456))
        self.assertEqual(WallTime.from_datetime(w.to_naive_datetime()), w)

    def test_invalid_calendar_date(self):
        with self.assertRaises(ValueError):
            WallTime.from_parts(2024, 2, 30)
        with self.assertRaises(ValueError):
            WallTime.from_parts(2024, 13, 1)

    def test_wall_arithmetic_preserves_time_of_day(self):
        w = WallTime.from_parts(2024, 3, 31, 23, 30)
        nxt = w.add_days(1)
        self.assertEqual(nxt, WallTime.from_parts(2024, 4, 1, 23, 30))
        back = w.add_days(-60)
        self.assertEqual(back, WallTime.from_parts(2024, 1, 31, 23, 30))

    def test_wall_subtraction_is_nominal(self):
        a = WallTime.from_parts(2024, 3, 9, 12)
        b = WallTime.from_parts(2024, 3, 11, 12)
        nominal = b - a
        self.assertEqual(nominal, Duration.of(days=2))
        self.assertEqual(nominal.total_seconds(), 2 * 86_400)

    def test_instant_and_duration_exact_microseconds(self):
        i = Instant(0) + Duration.of(hours=1, microseconds=5)
        self.assertEqual(i.epoch_us, 3600 * 1_000_000 + 5)
        j = i - Duration.of(hours=1)
        self.assertEqual(j, Instant(5))
        elapsed = i - j
        self.assertEqual(elapsed, Duration.of(hours=1))
        self.assertEqual(i.to_utc_datetime(), dt.datetime(1970, 1, 1, 1, 0, 0, 5))

    def test_no_float_on_timeline(self):
        d = Duration.of(seconds=1, microseconds=1)
        self.assertEqual(d.microseconds, 1_000_001)
        self.assertIsInstance(Instant(7).epoch_us, int)


if __name__ == "__main__":
    unittest.main()
