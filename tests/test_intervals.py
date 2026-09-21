import unittest

from coverage_ledger import intervals as iv


class IntervalTests(unittest.TestCase):
    def test_touching_halves_merge(self):
        self.assertEqual(iv.merge([(0, 4), (4, 8)]), [(0, 8)])

    def test_overlaps_merge_without_double_count(self):
        self.assertEqual(iv.merge([(0, 6), (4, 8)]), [(0, 8)])
        self.assertEqual(iv.length(iv.merge([(0, 6), (4, 8)])), 8)

    def test_gap_is_preserved(self):
        self.assertEqual(iv.merge([(0, 4), (5, 8)]), [(0, 4), (5, 8)])

    def test_clip_and_intersection(self):
        self.assertEqual(iv.clip([(0, None)], (2, 5)), [(2, 5)])
        self.assertEqual(iv.intersect_lists([(0, 6)], [(4, 8)]), [(4, 6)])
        self.assertEqual(iv.intersect_interval((0, None), (5, None)), (5, None))

    def test_subtract_and_symdiff(self):
        self.assertEqual(iv.subtract([(0, 10)], [(4, 6)]), [(0, 4), (6, 10)])
        self.assertEqual(iv.symmetric_difference([(0, 6)], [(0, 5)]), [(5, 6)])

    def test_invalid_intervals(self):
        with self.assertRaises(ValueError):
            iv.check_interval((5, 4))
        with self.assertRaises(ValueError):
            iv.check_interval((0, None))
        self.assertEqual(iv.is_empty((3, 3)), True)


if __name__ == "__main__":
    unittest.main()
