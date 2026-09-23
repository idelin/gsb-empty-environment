import unittest
from fractions import Fraction as F

from interval_arith import Interval, DivisionByZeroInterval, dependency_note


class TestBasicOps(unittest.TestCase):
    def test_add_sub_exact(self):
        a = Interval(F(1, 3), F(1, 2))
        b = Interval(F(1, 6), F(2, 3))
        self.assertEqual(a + b, Interval(F(1, 2), F(7, 6)))
        self.assertEqual(a - b, Interval(F(-1, 3), F(1, 3)))

    def test_mul_basic(self):
        self.assertEqual(Interval(-2, 3) * Interval(4, 5), Interval(-10, 15))
        self.assertEqual(Interval(-3, -1) * Interval(-2, 4), Interval(-12, 6))

    def test_mul_same_interval_is_conservative_and_noted(self):
        x = Interval(-1, 1)
        self.assertEqual(x * x, Interval(-1, 1))  # conservative, NOT [0,1]
        note = dependency_note(x, x)
        self.assertIsNotNone(note)
        self.assertIn("dependency", note)

    def test_square_is_tight(self):
        self.assertEqual(Interval(-1, 1).square(), Interval(0, 1))
        self.assertEqual(Interval(1, 2).square(), Interval(1, 4))
        self.assertEqual(Interval(-3, -1).square(), Interval(1, 9))
        self.assertEqual(Interval(-1, 2).square(), Interval(0, 4))

    def test_pow(self):
        self.assertEqual(Interval(-2, 3).pow(3), Interval(-8, 27))
        self.assertEqual(Interval(-2, 3).pow(2), Interval(0, 9))
        self.assertEqual(Interval(2, 3).pow(2), Interval(4, 9))

    def test_division_normal(self):
        self.assertEqual(Interval(1, 2) / Interval(1, 4), Interval(F(1, 4), 2))

    def test_division_across_zero_raises_not_hidden(self):
        with self.assertRaises(DivisionByZeroInterval) as ctx:
            Interval(1, 2) / Interval(-1, 2)
        self.assertEqual(ctx.exception.divisor, Interval(-1, 2))

    def test_division_with_zero_endpoint_raises(self):
        with self.assertRaises(DivisionByZeroInterval):
            Interval(1, 1) / Interval(0, 2)

    def test_reciprocal_split_gives_discontinuous_unbounded_pieces(self):
        pieces = Interval(-1, 2).reciprocal_split()
        self.assertEqual(len(pieces), 2)
        (sub_l, img_l), (sub_r, img_r) = pieces
        self.assertEqual(sub_l, Interval(-1, 0, hi_open=True))
        self.assertEqual(img_l, Interval(None, -1))          # (-inf, -1]
        self.assertEqual(sub_r, Interval(0, 2, lo_open=True))
        self.assertEqual(img_r, Interval(F(1, 2), None))     # [1/2, +inf)
        for sub, _ in pieces:
            self.assertFalse(sub.contains(0))  # singularity excluded

    def test_div_split(self):
        pieces = Interval(1, 2).div_split(Interval(-1, 2))
        images = [img for _, img in pieces]
        self.assertEqual(images[0], Interval(None, -1))
        self.assertEqual(images[1], Interval(F(1, 2), None))

    def test_reciprocal_split_no_singularity(self):
        pieces = Interval(1, 2).reciprocal_split()
        self.assertEqual(pieces, [(Interval(1, 2), Interval(F(1, 2), 1))])


class TestMonotone(unittest.TestCase):
    def test_cube_increasing(self):
        self.assertEqual(Interval(-2, 3).cube(), Interval(-8, 27))

    def test_reciprocal_decreasing(self):
        iv = Interval(1, 2).apply_monotone(lambda t: 1 / t, increasing=False, name="1/x")
        self.assertEqual(iv, Interval(F(1, 2), 1))

    def test_apply_monotone_exact_rational(self):
        iv = Interval(F(1, 2), F(3, 2)).apply_monotone(lambda t: 2 * t + 1)
        self.assertEqual(iv, Interval(2, 4))


class TestRounding(unittest.TestCase):
    def test_from_float_exact(self):
        iv = Interval.from_float(0.1)
        self.assertEqual(iv.lo, F(0.1))  # exact binary value, no hidden rounding

    def test_round_outward_is_conservative(self):
        iv = Interval(F(1, 3), F(2, 3)).round_outward(F(1, 10))
        self.assertEqual(iv, Interval(F(3, 10), F(7, 10)))
        self.assertTrue(iv.contains(F(1, 3)))
        self.assertTrue(iv.contains(F(2, 3)))
        self.assertTrue(Interval(F(1, 3), F(2, 3)).subset_of(iv))


class TestMembership(unittest.TestCase):
    def test_open_endpoints(self):
        iv = Interval(0, 2, lo_open=True)
        self.assertFalse(iv.contains(0))
        self.assertTrue(iv.contains(2))

    def test_unbounded_membership(self):
        self.assertTrue(Interval(None, -1).contains(-10**6))
        self.assertFalse(Interval(None, -1).contains(0))

    def test_subset_of(self):
        self.assertTrue(Interval(0, 1).subset_of(Interval(-1, 2)))
        self.assertFalse(Interval(0, 3).subset_of(Interval(-1, 2)))
        self.assertTrue(Interval(0, 1).subset_of(Interval(None, None)))


if __name__ == "__main__":
    unittest.main()
