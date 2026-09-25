"""时间模型测试：精确有理表示 + 三类时间严格区分。"""

import unittest
from fractions import Fraction

from clockalign.time_model import Duration, Instant, WallTime, to_fraction


class TestExactRepresentation(unittest.TestCase):
    def test_exact_rational_storage(self):
        d = Duration("0.1")
        self.assertEqual(d.seconds, Fraction(1, 10))
        w = WallTime(Fraction(1, 3))
        self.assertEqual(w.seconds, Fraction(1, 3))
        i = Instant("3600.25")
        self.assertEqual(i.seconds, Fraction(14401, 4))

    def test_no_float_accepted(self):
        with self.assertRaises(TypeError):
            to_fraction(0.1)
        with self.assertRaises(TypeError):
            Duration(0.5)
        with self.assertRaises(TypeError):
            WallTime(1.5)
        with self.assertRaises(TypeError):
            Instant(2.5)

    def test_arithmetic_is_exact(self):
        a = Duration("0.1") + Duration("0.2")
        self.assertEqual(a.seconds, Fraction(3, 10))
        self.assertEqual((Duration(1) / 3).seconds, Fraction(1, 3))
        self.assertEqual(Duration(7) / Duration(2), Fraction(7, 2))


class TestTypeSeparation(unittest.TestCase):
    def test_walltime_minus_walltime_is_duration(self):
        d = WallTime(10) - WallTime(3)
        self.assertIsInstance(d, Duration)
        self.assertEqual(d.seconds, Fraction(7))

    def test_instant_minus_instant_is_duration(self):
        d = Instant(100) - Instant("99.5")
        self.assertIsInstance(d, Duration)
        self.assertEqual(d.seconds, Fraction(1, 2))

    def test_walltime_not_comparable_to_instant(self):
        self.assertNotEqual(WallTime(5), Instant(5))
        with self.assertRaises(TypeError):
            WallTime(5) < Instant(5)  # noqa: B015
        with self.assertRaises(TypeError):
            Instant(5) < WallTime(5)  # noqa: B015

    def test_no_mixed_arithmetic(self):
        with self.assertRaises(TypeError):
            WallTime(5) + Instant(5)
        with self.assertRaises(TypeError):
            Instant(5) + Instant(5)
        with self.assertRaises(TypeError):
            Duration(1) + WallTime(1)

    def test_shift_by_duration(self):
        w = WallTime(10) + Duration("0.5")
        self.assertIsInstance(w, WallTime)
        self.assertEqual(w.seconds, Fraction(21, 2))
        i = Instant(10) - Duration("0.25")
        self.assertIsInstance(i, Instant)
        self.assertEqual(i.seconds, Fraction(39, 4))

    def test_ordering_within_type(self):
        self.assertLess(WallTime(1), WallTime(2))
        self.assertLess(Instant(1), Instant(2))
        self.assertLess(Duration(1), Duration(2))


class TestFormatting(unittest.TestCase):
    def test_exact_and_approx(self):
        self.assertEqual(Duration(Fraction(1, 3)).exact(), "1/3s")
        self.assertEqual(Duration(2).exact(), "2s")
        self.assertEqual(WallTime(5).exact(), "T+5s")
        self.assertEqual(Instant(Fraction(7, 2)).exact(), "R+7/2s")
        self.assertEqual(Duration(Fraction(1, 2)).approx(3), "0.500s")


if __name__ == "__main__":
    unittest.main()
