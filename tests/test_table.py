import unittest
from fractions import Fraction

from timecal import RoundingPolicy, Segment, SyntheticOffsetTable


class TableTest(unittest.TestCase):
    def setUp(self):
        # 1000 ticks at 1 ms/tick, then 500 ticks at 2 ms/tick, from t=1000s
        self.table = SyntheticOffsetTable.synthesize(
            0, 1000, [(1000, Fraction(1, 1000)), (500, Fraction(1, 500))]
        )

    def test_exact_forward_conversion(self):
        self.assertEqual(self.table.to_seconds(0), 1000)
        self.assertEqual(self.table.to_seconds(1000), 1001)  # continuous at rate change
        self.assertEqual(self.table.to_seconds(1500), 1002)
        self.assertEqual(
            self.table.to_seconds(1001), Fraction(1001) + Fraction(1, 500)
        )

    def test_exact_inverse_with_rounding_policy(self):
        self.assertEqual(self.table.to_ticks(1001), 1000)
        # 1001.0005 s is exactly tick 1000.25 -> half-even rounds to 1000
        self.assertEqual(self.table.to_ticks(Fraction(10010005, 10000)), 1000)
        self.assertEqual(
            self.table.to_ticks(Fraction(10010005, 10000), RoundingPolicy.CEILING),
            1001,
        )

    def test_inverse_may_have_multiple_solutions(self):
        sols = self.table.ticks_covering(1000, 1001)
        self.assertEqual((sols.start, sols.stop - 1), (0, 1000))
        self.assertEqual(len(list(sols)), 1001)

    def test_invalid_tables_rejected(self):
        with self.assertRaises(ValueError):
            SyntheticOffsetTable([])
        with self.assertRaises(ValueError):
            SyntheticOffsetTable([Segment(10, 1, 0), Segment(5, 1, 0)])


if __name__ == "__main__":
    unittest.main()
