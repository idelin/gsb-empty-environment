import math
import unittest

from interval_analysis.interval import BOTTOM, TOP, Interval


class TestIntervalBasics(unittest.TestCase):
    def test_const_and_contains(self):
        i = Interval.of(2, 5)
        self.assertTrue(i.contains(2))
        self.assertTrue(i.contains(5))
        self.assertFalse(i.contains(6))
        self.assertFalse(i.is_bottom)

    def test_bottom_canonical(self):
        self.assertTrue(Interval(3, 2).is_bottom)
        self.assertEqual(Interval(3, 2), BOTTOM)

    def test_join_meet(self):
        a, b = Interval.of(0, 4), Interval.of(2, 9)
        self.assertEqual(a.join(b), Interval.of(0, 9))
        self.assertEqual(a.meet(b), Interval.of(2, 4))
        self.assertEqual(a.join(BOTTOM), a)
        self.assertEqual(a.meet(BOTTOM), BOTTOM)

    def test_arithmetic(self):
        a, b = Interval.of(-2, 3), Interval.of(1, 4)
        self.assertEqual(a.add(b), Interval.of(-1, 7))
        self.assertEqual(a.sub(b), Interval.of(-6, 2))
        self.assertEqual(a.mul(b), Interval.of(-8, 12))
        self.assertEqual(a.neg(), Interval.of(-3, 2))

    def test_infinite_arithmetic(self):
        self.assertEqual(TOP.add(Interval.of(1, 1)), TOP)
        self.assertEqual(Interval.of(0, 0).mul(TOP), Interval.of(0, 0))
        self.assertEqual(Interval.of(1, 2).mul(TOP), TOP)

    def test_div_truncates_toward_zero(self):
        self.assertEqual(Interval.of(-7, -7).div(Interval.of(2, 2)),
                         Interval.of(-3, -3))
        self.assertEqual(Interval.of(7, 7).div(Interval.of(-2, -2)),
                         Interval.of(-3, -3))

    def test_div_skips_zero_divisor(self):
        # [4,8] / [-2,2] = ([4,8]/[-2,-1]) U ([4,8]/[1,2]) = [-8,-2] U [2,8]
        self.assertEqual(Interval.of(4, 8).div(Interval.of(-2, 2)),
                         Interval.of(-8, 8))
        self.assertEqual(Interval.of(1, 1).div(Interval.of(0, 0)), BOTTOM)

    def test_mod(self):
        self.assertEqual(Interval.of(0, 5).mod(Interval.of(3, 3)),
                         Interval.of(0, 2))
        self.assertEqual(Interval.of(-5, 5).mod(Interval.of(3, 3)),
                         Interval.of(-2, 2))
        self.assertEqual(Interval.of(1, 1).mod(Interval.of(0, 0)), BOTTOM)

    def test_widen_narrow(self):
        a, b = Interval.of(0, 1), Interval.of(0, 2)
        self.assertEqual(a.widen(b), Interval.of(0, math.inf))
        self.assertEqual(Interval.of(0, math.inf).narrow(Interval.of(0, 100)),
                         Interval.of(0, 100))
        self.assertEqual(a.widen(Interval.of(-1, 1)),
                         Interval.of(-math.inf, 1))


class TestAssume(unittest.TestCase):
    def test_neq_outside_is_precise_noop(self):
        refined, precise = Interval.of(-3, -1).assume_ne(Interval.const(0))
        self.assertEqual(refined, Interval.of(-3, -1))
        self.assertTrue(precise)

    def test_neq_at_boundary_is_exact(self):
        refined, precise = Interval.of(0, 5).assume_ne(Interval.const(0))
        self.assertEqual(refined, Interval.of(1, 5))
        self.assertTrue(precise)
        refined, precise = Interval.of(-5, 0).assume_ne(Interval.const(0))
        self.assertEqual(refined, Interval.of(-5, -1))
        self.assertTrue(precise)

    def test_neq_singleton_to_bottom(self):
        refined, precise = Interval.const(0).assume_ne(Interval.const(0))
        self.assertTrue(refined.is_bottom)
        self.assertTrue(precise)

    def test_neq_straddling_zero_keeps_zero_and_is_imprecise(self):
        # The exact result would be [-3,-1] U [1,3]; the domain cannot
        # represent the hole, so it must keep the sound interval and say so.
        refined, precise = Interval.of(-3, 3).assume_ne(Interval.const(0))
        self.assertEqual(refined, Interval.of(-3, 3))
        self.assertTrue(refined.contains(0))
        self.assertFalse(precise)

    def test_order_refinements(self):
        self.assertEqual(Interval.of(0, 10).assume_lt(Interval.const(4))[0],
                         Interval.of(0, 3))
        self.assertEqual(Interval.of(0, 10).assume_le(Interval.const(4))[0],
                         Interval.of(0, 4))
        self.assertEqual(Interval.of(0, 10).assume_gt(Interval.const(4))[0],
                         Interval.of(5, 10))
        self.assertEqual(Interval.of(0, 10).assume_ge(Interval.const(4))[0],
                         Interval.of(4, 10))
        self.assertTrue(Interval.of(0, 2).assume_lt(Interval.const(0))[0].is_bottom)


if __name__ == "__main__":
    unittest.main()
