import unittest
from fractions import Fraction

from timeproj.blocks import SamplingBlock
from timeproj.compose import compose, project_slices
from timeproj.projection import project
from timeproj.types import Interval, TimePoint


def tp(v):
    return TimePoint(Fraction(v))


class TestCompose(unittest.TestCase):
    def setUp(self):
        self.a = [SamplingBlock(0, 0, tp(0), Fraction(1), tp(0), tp(100))]
        self.b = [SamplingBlock(0, 0, tp(0), Fraction(1, 2), tp(0), tp(100))]
        self.c = [SamplingBlock(0, 0, tp(0), Fraction(1, 3), tp(0), tp(100))]

    def test_two_layer_composition_matches_direct_mapping(self):
        q = Interval(tp(3), tp(9))
        composed = compose(q, self.a, self.b)
        direct = project(q, self.b)
        self.assertEqual(composed.slices, direct.slices)
        self.assertEqual(composed.gaps, direct.gaps)
        self.assertEqual(composed.missing, direct.missing)
        self.assertEqual(composed.dedupe, direct.dedupe)

    def test_rounded_query_composition_matches_covered_interval(self):
        q = Interval(tp(Fraction(7, 2)), tp(9))
        composed = compose(q, self.a, self.b)
        # A's grid rounds the query to the covered interval [4, 9]
        direct = project(Interval(tp(4), tp(9)), self.b)
        self.assertEqual(composed.slices, direct.slices)
        # here it also coincides with the direct projection of the raw query
        self.assertEqual(composed.slices, project(q, self.b).slices)

    def test_three_layer_composition_matches_direct_mapping(self):
        q = Interval(tp(6), tp(30))  # aligned to all three grids
        via_ab = compose(q, self.a, self.b)
        via_abc = project_slices(via_ab, self.c)
        direct = compose(q, self.a, self.c)
        self.assertEqual(via_abc.slices, direct.slices)
        self.assertEqual(via_abc.slices, project(q, self.c).slices)

    def test_composition_propagates_gaps_and_missing(self):
        a = [SamplingBlock(0, 0, tp(0), Fraction(1), tp(0), tp(10),
                           frozenset({3}))]
        b = [SamplingBlock(0, 0, tp(0), Fraction(1), tp(6), tp(20))]
        q = Interval(tp(0), tp(10), end_open=True)
        composed = compose(q, a, b)
        # A covers [0,10) but drops t=3; B only covers [6,20)
        self.assertEqual(composed.sample_count, 4)  # t = 6,7,8,9
        covered_by_b = [g.interval for g in composed.gaps]
        self.assertTrue(any(g.start.value == Fraction(0)
                            for g in covered_by_b))


if __name__ == "__main__":
    unittest.main()
