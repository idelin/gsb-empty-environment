import unittest
from datetime import date, timedelta
from fractions import Fraction

from calpart import Duration, Instant, Wall
from calpart.exact import civil_from_days, days_from_civil


class TestCivilConversion(unittest.TestCase):
    def test_matches_datetime_across_range(self):
        epoch = date(1970, 1, 1)
        start_days = (date(1, 1, 1) - epoch).days
        end_days = (date(9999, 12, 31) - epoch).days
        step = 0
        for days in range(start_days, end_days + 1, 997):
            probe = epoch + timedelta(days=days)
            self.assertEqual(days_from_civil(probe.year, probe.month, probe.day), days)
            self.assertEqual(civil_from_days(days), (probe.year, probe.month, probe.day))
            step += 1
        self.assertGreater(step, 3000)

    def test_supports_years_beyond_datetime(self):
        year, month, day = civil_from_days(days_from_civil(102024, 1, 1))
        self.assertEqual((year, month, day), (102024, 1, 1))
        self.assertEqual(days_from_civil(2025, 1, 1) - days_from_civil(2024, 1, 1), 366)


class TestExactTypes(unittest.TestCase):
    def test_civil_constructor_is_exact(self):
        self.assertEqual(Instant.from_civil(1970, 1, 2).seconds, 86400)
        self.assertEqual(
            Instant.from_civil(2024, 2, 29, 12, 30, 15).seconds,
            Instant.from_civil(2024, 3, 1).seconds - Duration(41385).seconds,
        )

    def test_instant_duration_arithmetic(self):
        t0 = Instant.from_civil(2024, 10, 27)
        t1 = t0 + Duration(3600)
        self.assertIsInstance(t1, Instant)
        self.assertEqual((t1 - t0).seconds, 3600)
        self.assertIsInstance(t1 - t0, Duration)

    def test_wall_is_a_distinct_type(self):
        w0 = Wall.from_civil(2024, 10, 27, 8)
        w1 = w0 + Duration(1800)
        self.assertIsInstance(w1, Wall)
        self.assertEqual((w1 - w0).seconds, 1800)

    def test_mixed_type_arithmetic_is_rejected(self):
        with self.assertRaises(TypeError):
            Instant(0) + Instant(1)
        with self.assertRaises(TypeError):
            Instant(0) - Wall(0)
        with self.assertRaises(TypeError):
            Wall(0) + Wall(0)
        with self.assertRaises(TypeError):
            Duration(1) + Instant(0)

    def test_exact_fractional_endpoints(self):
        t = Instant(Fraction(1, 3)) + Duration(Fraction(1, 3))
        self.assertEqual(t.seconds, Fraction(2, 3))
        self.assertNotEqual(t.seconds, 0.6666666666666666)

    def test_float_is_rejected_as_inexact(self):
        with self.assertRaises(TypeError):
            Instant(0.5)
        with self.assertRaises(TypeError):
            Duration(1.5)

    def test_duration_arithmetic(self):
        d = Duration(30) + Duration(90)
        self.assertEqual(d.seconds, 120)
        self.assertEqual((d * 3).seconds, 360)
        self.assertEqual((-Duration(5)).seconds, -5)

    def test_ordering(self):
        self.assertLess(Instant(10), Instant(20))
        self.assertLess(Wall(10), Wall(20))
        self.assertLess(Duration(1), Duration(2))


if __name__ == "__main__":
    unittest.main()
