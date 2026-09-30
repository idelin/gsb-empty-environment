import unittest
from fractions import Fraction

from timecal import (
    MICROSECOND,
    MILLISECOND,
    SECOND,
    TimeUnit,
    compare,
    convert_exact,
    convert_rounded,
)


class UnitConversionTest(unittest.TestCase):
    def test_ms_us_and_tick_units_unified(self):
        tick = TimeUnit.sampling(44100)
        self.assertEqual(convert_exact(1500, MILLISECOND, SECOND), Fraction(3, 2))
        self.assertEqual(convert_exact(1, SECOND, MICROSECOND), 1_000_000)
        self.assertEqual(convert_exact(44100, tick, SECOND), 1)
        self.assertEqual(convert_exact(1, tick, SECOND), Fraction(1, 44100))

    def test_third_of_second_to_us_half_even(self):
        self.assertEqual(
            convert_rounded(Fraction(1, 3), SECOND, MICROSECOND), 333333
        )
        self.assertEqual(
            convert_rounded(Fraction(1, 6), SECOND, MICROSECOND), 166667
        )

    def test_compare_is_exact_not_float(self):
        big = 2**70
        # floats genuinely cannot tell these two apart
        self.assertEqual(float(big), float(big) + 1e-6)
        self.assertEqual(compare(big, SECOND, big * 1_000_000 + 1, MICROSECOND), -1)
        self.assertEqual(compare(big, SECOND, big * 1_000_000, MICROSECOND), 0)
        self.assertEqual(compare(1, SECOND, 1000, MILLISECOND), 0)
        self.assertEqual(compare(Fraction(1, 3), SECOND, 333333, MICROSECOND), 1)
        self.assertEqual(compare(Fraction(1, 3), SECOND, 333334, MICROSECOND), -1)


if __name__ == "__main__":
    unittest.main()
