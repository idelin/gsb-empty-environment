import unittest

from timeindex import Interval


class IntervalTests(unittest.TestCase):
    def test_open_closed_emptiness(self):
        self.assertTrue(Interval(1, 1, True, False).is_empty())
        self.assertTrue(Interval(1, 1, False, True).is_empty())
        self.assertFalse(Interval(1, 1, True, True).is_empty())
        self.assertTrue(Interval(2, 1).is_empty())

    def test_contains_respects_openness(self):
        self.assertTrue(Interval(0, 1, True, True).contains(1))
        self.assertFalse(Interval(0, 1, True, False).contains(1))
        self.assertFalse(Interval(0, 1, False, True).contains(0))
        self.assertTrue(Interval(None, 0).contains(-10**9))

    def test_intersect_merges_bounds(self):
        a = Interval(0, 10, False, True)
        b = Interval(0, 5, True, False)
        self.assertEqual(a.intersect(b), Interval(0, 5, False, False))
        self.assertTrue(a.intersect(Interval(20, 30)).is_empty())

    def test_difference_two_pieces(self):
        pieces = Interval(0, 10).difference(Interval(3, 5))
        self.assertEqual(pieces, [Interval(0, 3, True, False),
                                  Interval(5, 10, False, True)])

    def test_difference_open_boundary_keeps_point(self):
        pieces = Interval(0, 10).difference(Interval(0, 5, False, False))
        self.assertEqual(pieces, [Interval(5, 10, True, True)])

    def test_difference_empty_other_is_identity(self):
        self.assertEqual(Interval(0, 10).difference(Interval(3, 3, True, False)),
                         [Interval(0, 10)])

    def test_difference_unbounded(self):
        pieces = Interval(None, None).difference(Interval(0, 1))
        self.assertEqual(pieces, [Interval(None, 0, True, False),
                                  Interval(1, None, False, True)])


if __name__ == "__main__":
    unittest.main()
