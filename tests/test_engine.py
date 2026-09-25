"""Engine semantics: groups, exclusions, radius, ties, dedup, deletion."""

import unittest
from fractions import Fraction as F

from engine import Engine, MODE_ALL, MODE_REPRESENTATIVE


def make_engine():
    eng = Engine()
    for name in ("A", "B", "C"):
        eng.add_group(name)
    return eng


class TestBasicQueries(unittest.TestCase):
    def test_crossing_distance_zero(self):
        eng = make_engine()
        eng.set_segment("A", 1, (0, 0), (3, 0))
        eng.set_segment("B", 7, (1, -1), (1, 2))
        res = eng.query("A", "B")
        self.assertTrue(res.found)
        self.assertEqual(res.dist2, 0)
        self.assertEqual(len(res.evidence), 1)
        ev = res.evidence[0]
        self.assertEqual((ev.group_a, ev.seg_a, ev.group_b, ev.seg_b),
                         ("A", 1, "B", 7))
        self.assertEqual(ev.param_a, (F(1, 3), F(1, 3)))
        self.assertEqual(ev.param_b, (F(1, 3), F(1, 3)))
        self.assertEqual(ev.point_a[0], (F(1), F(0)))

    def test_delete_nearest_finds_next_candidate(self):
        eng = make_engine()
        # Group B candidates at squared distances 1, 4, 9 from A.
        eng.set_segment("A", 1, (0, 0), (10, 0))
        eng.set_segment("B", 1, (3, 1), (4, 1))
        eng.set_segment("B", 2, (3, 2), (4, 2))
        eng.set_segment("B", 3, (3, -3), (4, -3))
        res = eng.query("A", "B")
        self.assertEqual(res.dist2, 1)
        self.assertEqual(res.evidence[0].seg_b, 1)
        eng.remove_segment("B", 1)
        res = eng.query("A", "B")
        self.assertEqual(res.dist2, 4)
        self.assertEqual(res.evidence[0].seg_b, 2)
        eng.remove_segment("B", 2)
        res = eng.query("A", "B")
        self.assertEqual(res.dist2, 9)
        self.assertEqual(res.evidence[0].seg_b, 3)

    def test_shared_endpoint_not_duplicated(self):
        eng = make_engine()
        # Polyline in A: two segments sharing vertex (2,0).
        eng.set_segment("A", 1, (0, 0), (2, 0))
        eng.set_segment("A", 2, (2, 0), (2, 2))
        # Zero-length segment in B acts as a point.
        eng.set_segment("B", 9, (5, 0), (5, 0))
        res = eng.query("A", "B")
        self.assertEqual(res.dist2, 9)
        # Both (seg1, 9) and (seg2, 9) yield the same point pair
        # ((2,0),(5,0)); dedup keeps exactly one record, the one with
        # the smallest segment id.
        self.assertEqual(len(res.evidence), 1)
        ev = res.evidence[0]
        self.assertEqual(ev.seg_a, 1)
        self.assertEqual(ev.point_a[0], (F(2), F(0)))
        self.assertEqual(ev.point_b[0], (F(5), F(0)))

    def test_zero_length_segment_as_point(self):
        eng = make_engine()
        eng.set_segment("A", 1, (3, 4), (3, 4))  # a point
        eng.set_segment("B", 1, (0, 0), (0, 2))
        res = eng.query("A", "B")
        self.assertEqual(res.dist2, 13)
        self.assertEqual(res.evidence[0].param_a, (F(0), F(0)))
        self.assertEqual(res.evidence[0].param_b, (F(1), F(1)))

    def test_all_tied_evidence_reported(self):
        eng = make_engine()
        eng.set_segment("A", 1, (0, 0), (10, 0))
        eng.set_segment("B", 1, (2, 1), (3, 1))
        eng.set_segment("B", 2, (5, -1), (6, -1))
        eng.set_segment("C", 1, (8, 1), (9, 1))
        res = eng.query_all()
        self.assertEqual(res.dist2, 1)
        got = {(ev.group_a, ev.seg_a, ev.group_b, ev.seg_b)
               for ev in res.evidence}
        self.assertEqual(got, {("A", 1, "B", 1), ("A", 1, "B", 2),
                               ("A", 1, "C", 1)})


class TestParallelModes(unittest.TestCase):
    def setUp(self):
        self.eng = make_engine()
        self.eng.set_segment("A", 1, (0, 0), (4, 0))
        self.eng.set_segment("B", 1, (1, 1), (3, 1))

    def test_all_mode_reports_interval(self):
        res = self.eng.query("A", "B", mode=MODE_ALL)
        self.assertEqual(res.dist2, 1)
        self.assertEqual(len(res.evidence), 1)
        ev = res.evidence[0]
        self.assertEqual(ev.param_a, (F(1, 4), F(3, 4)))
        self.assertEqual(ev.param_b, (F(0), F(1)))
        self.assertEqual(ev.point_a, ((F(1), F(0)), (F(3), F(0))))
        self.assertEqual(ev.point_b, ((F(1), F(1)), (F(3), F(1))))

    def test_representative_mode_stable_point(self):
        res = self.eng.query("A", "B", mode=MODE_REPRESENTATIVE)
        self.assertEqual(res.dist2, 1)
        self.assertEqual(len(res.evidence), 1)
        ev = res.evidence[0]
        # Lexicographically smallest parameter pair of the continuum.
        self.assertEqual(ev.param_a, (F(1, 4), F(1, 4)))
        self.assertEqual(ev.param_b, (F(0), F(0)))
        self.assertEqual(ev.point_a[0], (F(1), F(0)))
        self.assertEqual(ev.point_b[0], (F(1), F(1)))
        # Stability: identical result on repeated queries.
        again = self.eng.query("A", "B", mode=MODE_REPRESENTATIVE)
        self.assertEqual(res, again)


class TestExclusionsAndRadius(unittest.TestCase):
    def test_exclusion_skips_pair(self):
        eng = make_engine()
        eng.set_segment("A", 1, (0, 0), (1, 0))
        eng.set_segment("B", 1, (0, 1), (1, 1))
        eng.set_segment("C", 1, (0, 3), (1, 3))
        self.assertEqual(eng.query_all().dist2, 1)
        eng.set_exclusion("A", "B")
        # A-B excluded: global best is now B-C at dist2 = 4, and the
        # next-best candidate for A is C at dist2 = 9.
        res = eng.query_all()
        self.assertEqual(res.dist2, 4)
        self.assertEqual((res.evidence[0].group_a, res.evidence[0].group_b),
                         ("B", "C"))
        self.assertEqual(eng.query("A", "C").dist2, 9)
        # Direct query of an excluded pair is defined to be empty.
        self.assertFalse(eng.query("A", "B").found)
        eng.clear_exclusion("A", "B")
        self.assertEqual(eng.query_all().dist2, 1)

    def test_radius_threshold(self):
        eng = make_engine()
        eng.set_segment("A", 1, (0, 0), (1, 0))
        eng.set_segment("B", 1, (0, 2), (1, 2))
        self.assertFalse(eng.query("A", "B", radius=F(3, 2)).found)
        res = eng.query("A", "B", radius=2)
        self.assertTrue(res.found)
        self.assertEqual(res.dist2, 4)
        res = eng.query("A", "B", radius=F(5, 2))
        self.assertEqual(res.dist2, 4)

    def test_intra_group_query_rejected(self):
        eng = make_engine()
        with self.assertRaises(ValueError):
            eng.query("A", "A")

    def test_empty_group_yields_empty_result(self):
        eng = make_engine()
        eng.set_segment("A", 1, (0, 0), (1, 0))
        self.assertFalse(eng.query("A", "B").found)
        self.assertFalse(eng.query_all().found)


if __name__ == "__main__":
    unittest.main()
