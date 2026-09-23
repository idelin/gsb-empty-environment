import unittest
from fractions import Fraction

from ratpiece import Interval, PiecewiseModel, Poly, RationalFunc, check_continuity


def rf(num, den=(1,)):
    return RationalFunc(Poly(num), Poly(den))


class TestContinuity(unittest.TestCase):
    def test_jump_reported(self):
        f = PiecewiseModel("f")
        f.set_piece(Interval.open(None, 0), RationalFunc.identity(), "l")
        f.set_piece(Interval.closed_open(0, 2), rf((2, 1)), "r")
        rep = check_continuity(f)
        jump = rep.boundaries[0]
        self.assertEqual(jump.kind, "jump")
        self.assertEqual(jump.left.value, Fraction(0))
        self.assertEqual(jump.right.value, Fraction(2))
        self.assertIn("跳变", jump.detail)

    def test_continuous_across_boundary(self):
        f = PiecewiseModel("f")
        f.set_piece(Interval.open(None, 1), RationalFunc.identity(), "l")
        f.set_piece(Interval.closed_open(1, None), RationalFunc.identity(), "r")
        rep = check_continuity(f)
        self.assertEqual(rep.boundaries[0].kind, "continuous")

    def test_infinite_discontinuity(self):
        g = PiecewiseModel("g")
        g.set_piece(Interval.open(0, 2), rf((1,), (-1, 1)), "1/(x-1)")
        rep = check_continuity(g)
        at_one = [b for b in rep.boundaries if b.point == 1][0]
        self.assertEqual(at_one.kind, "infinite")
        self.assertEqual(at_one.left.sign, -1)
        self.assertEqual(at_one.right.sign, 1)

    def test_removable_hole(self):
        h = PiecewiseModel("h")
        h.set_piece(Interval.all(), rf((-1, 0, 1), (-1, 1)), "hole")
        rep = check_continuity(h)
        self.assertEqual(rep.boundaries[0].kind, "removable")
        self.assertFalse(rep.boundaries[0].defined)

    def test_one_sided_not_claimed_continuous(self):
        g = PiecewiseModel("g")
        g.set_piece(Interval.open(0, 2), RationalFunc.identity(), "seg")
        rep = check_continuity(g)
        kinds = {b.point: b.kind for b in rep.boundaries}
        self.assertEqual(kinds[Fraction(0)], "one_sided")
        self.assertEqual(kinds[Fraction(2)], "one_sided")
        for b in rep.boundaries:
            self.assertNotEqual(b.kind, "continuous")

    def test_one_sided_endpoint_closed(self):
        g = PiecewiseModel("g")
        g.set_piece(Interval.closed(0, 2), RationalFunc.identity(), "seg")
        rep = check_continuity(g)
        at_zero = [b for b in rep.boundaries if b.point == 0][0]
        self.assertEqual(at_zero.kind, "one_sided")
        self.assertTrue(at_zero.defined)
        self.assertIn("右侧连续", at_zero.detail)

    def test_irrational_pole_signs(self):
        k = PiecewiseModel("k")
        k.set_piece(Interval.open(-3, 3), rf((1,), (-2, 0, 1)), "1/(x^2-2)")
        rep = check_continuity(k)
        poles = [s for s in rep.singularities if s.kind == "irrational_pole"]
        self.assertEqual(len(poles), 2)
        neg, pos = poles
        self.assertLess(neg.hi, 0)
        self.assertEqual((neg.left_sign, neg.right_sign), (1, -1))
        self.assertEqual((pos.left_sign, pos.right_sign), (-1, 1))
        self.assertFalse(rep.has_undecided())

    def test_budget_exhaustion_keeps_undecided(self):
        k = PiecewiseModel("k")
        k.set_piece(Interval.open(-3, 3), rf((1,), (-2, 0, 1)), "1/(x^2-2)")
        rep = check_continuity(k, budget=0)
        self.assertTrue(rep.has_undecided())
        kinds = [s.kind for s in rep.singularities]
        self.assertIn("undecided", kinds)

    def test_evidence_present(self):
        f = PiecewiseModel("f")
        f.set_piece(Interval.open(None, 0), RationalFunc.identity(), "l")
        f.set_piece(Interval.closed_open(0, 2), rf((2, 1)), "r")
        rep = check_continuity(f)
        for b in rep.boundaries:
            self.assertTrue(b.evidence)

    def test_value_mismatch_is_removable(self):
        f = PiecewiseModel("f")
        f.set_piece(Interval.open(None, 0), RationalFunc.constant(1), "l")
        f.set_piece(Interval.point(0), RationalFunc.constant(5), "pt")
        f.set_piece(Interval.open(0, None), RationalFunc.constant(1), "r")
        rep = check_continuity(f)
        at_zero = rep.boundaries[0]
        self.assertEqual(at_zero.kind, "removable")
        self.assertTrue(at_zero.defined)
        self.assertEqual(at_zero.value, Fraction(5))


if __name__ == "__main__":
    unittest.main()

