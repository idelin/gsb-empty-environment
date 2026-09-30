import unittest
from fractions import Fraction

from timecal import CalibratedMap, ClockGraph, NoPathError, integer_preimages


class AffineMapTest(unittest.TestCase):
    def test_compose_and_inverse_are_exact(self):
        m = CalibratedMap(Fraction(3, 7), Fraction(-2, 5))
        inv = m.inverse()
        self.assertTrue(m.then(inv).is_identity())
        self.assertTrue(inv.then(m).is_identity())
        self.assertEqual(m.apply(Fraction(7, 3)).center, Fraction(3, 5))

    def test_error_bounds_give_interval(self):
        m = CalibratedMap(Fraction(2), Fraction(3)).with_error("e1", Fraction(1, 1000))
        y = m.apply(10)
        self.assertEqual(y.center, 23)
        iv = y.interval()
        self.assertEqual(iv.lo, Fraction(23) - Fraction(1, 1000))
        self.assertEqual(iv.hi, Fraction(23) + Fraction(1, 1000))

    def test_same_source_error_correlation_cancels(self):
        m = CalibratedMap(Fraction(2), Fraction(3)).with_error("e1", Fraction(1, 1000))
        self.assertTrue(m.then(m.inverse()).is_identity())
        y = m.apply(10)
        x = m.preimage(y)
        self.assertEqual(x.center, 10)
        self.assertEqual(x.radius, 0)  # correlated error cancels exactly

    def test_independent_sources_accumulate(self):
        ab = CalibratedMap(Fraction(2), Fraction(0)).with_error("eAB", Fraction(1, 1000))
        bc = CalibratedMap(Fraction(1, 2), Fraction(0)).with_error("eBC", Fraction(1, 500))
        composed = ab.then(bc)
        self.assertEqual(composed.scale, 1)
        y = composed.apply(5)
        self.assertEqual(y.center, 5)
        self.assertEqual(y.radius, Fraction(1, 2000) + Fraction(1, 500))


def consistent_graph():
    g = ClockGraph()
    g.set_calibration("A", "B", CalibratedMap(Fraction(2), Fraction(1)))
    g.set_calibration("B", "C", CalibratedMap(Fraction(3), Fraction(-1)))
    g.set_calibration("C", "A", CalibratedMap(Fraction(1, 6), Fraction(-1, 3)))
    return g


class CycleTest(unittest.TestCase):
    def test_consistent_cycle_composes_to_identity(self):
        self.assertEqual(consistent_graph().view().check_cycles(), ())

    def test_contradiction_reports_evidence(self):
        g = consistent_graph()
        g.set_calibration("C", "A", CalibratedMap(Fraction(1, 6), Fraction(0)))
        cons = g.view().check_cycles()
        self.assertTrue(cons)
        c = cons[0]
        self.assertEqual(c.cycle[0], c.cycle[-1])
        self.assertEqual(c.offset_residual, Fraction(1, 3))
        self.assertIn("offset residual", c.describe())


class MultiPathTest(unittest.TestCase):
    def test_consistent_paths(self):
        g = consistent_graph()
        g.set_calibration("A", "C", CalibratedMap(Fraction(6), Fraction(2)))
        self.assertTrue(g.view().compare_paths("A", "C").consistent)

    def test_discrepant_paths_report_difference(self):
        g = consistent_graph()
        g.set_calibration("A", "C", CalibratedMap(Fraction(6), Fraction(5, 2)))
        comp = g.view().compare_paths("A", "C")
        self.assertFalse(comp.consistent)
        self.assertEqual(comp.reference_path, ("A", "C"))
        offsets = {d.difference.offset for d in comp.discrepancies}
        self.assertEqual(offsets, {Fraction(1, 2)})

    def test_no_path_raises(self):
        g = consistent_graph()
        with self.assertRaises(NoPathError):
            g.view().map("A", "Z")


class CacheVersionTest(unittest.TestCase):
    def test_local_correction_invalidates_selectively(self):
        g = ClockGraph()
        g.set_calibration("A", "B", CalibratedMap(Fraction(2), Fraction(1)))  # v1
        g.set_calibration("B", "C", CalibratedMap(Fraction(3)))               # v2
        g.set_calibration("X", "Y", CalibratedMap(Fraction(5)))               # v3
        v3 = g.view()
        self.assertEqual(v3.map("A", "C").scale, 6)
        v3.map("X", "Y")
        g.set_calibration("B", "C", CalibratedMap(Fraction(4)))               # v4
        self.assertEqual(g.version, 4)
        # only cache entries depending on the corrected edge are dropped
        self.assertNotIn(("A", "C"), g.cache_keys())
        self.assertIn(("X", "Y"), g.cache_keys())
        self.assertEqual(g.view().map("A", "C").scale, 8)
        # old versions are untouched
        self.assertEqual(g.at_version(3).map("A", "C").scale, 6)
        self.assertIn(("A", "C"), g.cache_keys(3))


class PreimageTest(unittest.TestCase):
    def test_inverse_may_have_multiple_solutions(self):
        cal = CalibratedMap(Fraction(2), Fraction(1))
        self.assertEqual(list(integer_preimages(cal, 5, 9)), [2, 3, 4])

    def test_error_bounds_widen_solution_set(self):
        cal = CalibratedMap(Fraction(2), Fraction(1)).with_error("e", Fraction(2))
        self.assertEqual(list(integer_preimages(cal, 5, 9)), [1, 2, 3, 4, 5])


if __name__ == "__main__":
    unittest.main()
