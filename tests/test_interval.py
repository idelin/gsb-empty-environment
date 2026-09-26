import unittest

from rational_pieces.interval import Interval


class TestInterval(unittest.TestCase):
    def test_intersect_open_closed(self):
        a = Interval(0, 2, True, True)
        b = Interval(1, 3, False, False)
        iv = a.intersect(b)
        self.assertEqual((iv.lo, iv.hi, iv.lo_closed, iv.hi_closed),
                         (1, 2, False, True))

    def test_touching_open_empty(self):
        self.assertIsNone(Interval(0, 1, True, False).intersect(
            Interval(1, 2, False, True)))

    def test_touching_closed_point(self):
        iv = Interval(0, 1, True, True).intersect(Interval(1, 2, True, True))
        self.assertIsNotNone(iv)
        self.assertEqual((iv.lo, iv.hi), (1, 1))
        self.assertTrue(iv.lo_closed and iv.hi_closed)

    def test_infinite_endpoints(self):
        iv = Interval(None, 0, False, True).intersect(Interval(-1, None, True, False))
        self.assertEqual((iv.lo, iv.hi), (-1, 0))
        self.assertTrue(iv.lo_closed and iv.hi_closed)

    def test_contains(self):
        iv = Interval(0, 1, True, False)
        self.assertTrue(iv.contains(0))
        self.assertFalse(iv.contains(1))
        self.assertTrue(Interval.real_line().contains(10**9))


if __name__ == '__main__':
    unittest.main()
