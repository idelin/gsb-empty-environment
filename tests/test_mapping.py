import unittest
from fractions import Fraction

from timemap import Interval, IntervalSet, PiecewiseMap


def make_map():
    """play [0,5) -> pause [5,10) -> 2x [10,20) -> reverse [20,30]."""
    return PiecewiseMap.build(0, 0, [(5, 1), (5, 0), (10, 2), (10, -1)])


class TestMapping(unittest.TestCase):
    def setUp(self):
        self.m = make_map()

    def test_segment_kinds(self):
        kinds = [s.kind for s in self.m.segments]
        self.assertEqual(kinds, ["play", "pause", "speed x2", "reverse"])

    def test_pause_bounds_share_one_logical_point(self):
        self.assertEqual(self.m.eval(5), self.m.eval(10))
        self.assertEqual(self.m.eval(5), 5)

    def test_eval_reverse_and_speed(self):
        self.assertEqual(self.m.eval(12), 9)
        self.assertEqual(self.m.eval(20), 25)
        self.assertEqual(self.m.eval(30), 15)

    def test_image_full_domain(self):
        img = self.m.image(self.m.domain())
        self.assertEqual(str(img), "[0, 25]")

    def test_image_subinterval_openness_kept(self):
        img = self.m.image(Interval(0, 5, hi_open=True))
        self.assertEqual(str(img), "[0, 5)")

    def test_preimage_of_pause_value_returns_whole_segment_with_bounds(self):
        pre = self.m.preimage(Interval.point(5))
        self.assertEqual(str(pre), "[5, 10]")
        self.assertIn(5, pre)
        self.assertIn(10, pre)

    def test_preimage_constant_segment_whole_domain(self):
        pre = self.m.preimage(Interval(5, 6, hi_open=True))
        self.assertIn(7, pre)
        self.assertIn(5, pre)

    def test_preimage_across_reverse_and_forward(self):
        pre = self.m.preimage(Interval(20, 25, hi_open=False))
        self.assertEqual(str(pre), "[35/2, 25]")
        self.assertEqual(len(self.m.segments), 4)

    def test_preimage_multiple_components(self):
        pre = self.m.preimage(Interval.point(20))
        self.assertEqual(str(pre), "{35/2} U {25}")

    def test_huge_domain_no_tick_expansion(self):
        big = 10**15
        m = PiecewiseMap.build(0, 0, [(big, 1), (big, 0), (big, 3)])
        self.assertEqual(len(m.segments), 3)
        img = m.image(m.domain())
        self.assertEqual(str(img), f"[0, {4 * big}]")
        pre = m.preimage(Interval.point(big))
        self.assertEqual(str(pre), f"[{big}, {2 * big}]")
        self.assertEqual(m.eval(Fraction(5 * big, 2)), Fraction(5 * big, 2))


if __name__ == "__main__":
    unittest.main()
