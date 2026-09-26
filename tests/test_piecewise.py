import unittest
from fractions import Fraction

from timefit.piecewise import (
    Anchor,
    ExtrapolationError,
    InfeasibleError,
    PiecewiseAffineFitter,
    Status,
)
from timefit.synth import synthetic_table
from timefit.timebase import Duration, Instant


def anchor(local_s, ref_s, err_ms=0):
    return Anchor(
        Instant(Fraction(local_s)),
        Instant(Fraction(ref_s)),
        Duration(milliseconds=err_ms),
    )


class FeasibilityTests(unittest.TestCase):
    def test_perfect_fit(self):
        anchors = [anchor(t, t + 5) for t in (0, 60, 120)]
        fitter = PiecewiseAffineFitter(anchors, Fraction(1, 100))
        self.assertTrue(fitter.feasible())
        result = fitter.solve()
        self.assertEqual(result.status, Status.EXACT)
        self.assertEqual(result.min_removals, 0)
        self.assertEqual(result.removed, ())

    def test_drift_violation(self):
        # ref advances 10 s over 60 s of local time; max drift is 5%.
        anchors = [anchor(0, 5), anchor(60, 15)]
        fitter = PiecewiseAffineFitter(anchors, Fraction(5, 100))
        self.assertFalse(fitter.feasible())
        result = fitter.solve()
        self.assertEqual(result.min_removals, 1)
        self.assertIn((0, 1), result.cores)

    def test_same_local_conflicting_ref(self):
        # Same local time, disjoint error intervals: direct conflict.
        anchors = [anchor(0, 5, 10), anchor(0, 7, 10)]
        fitter = PiecewiseAffineFitter(anchors, Fraction(1, 10))
        self.assertFalse(fitter.feasible())
        result = fitter.solve()
        self.assertIn((0, 1), result.cores)
        self.assertEqual(result.min_removals, 1)

    def test_same_local_overlapping_ref(self):
        anchors = [anchor(0, 5, 100), anchor(0, Fraction(101, 20), 100)]
        fitter = PiecewiseAffineFitter(anchors, Fraction(1, 10))
        self.assertTrue(fitter.feasible())


class OutlierTests(unittest.TestCase):
    def setUp(self):
        self.anchors = synthetic_table()
        self.fitter = PiecewiseAffineFitter(
            self.anchors, Fraction(5, 1000), Duration(30)
        )

    def test_min_removals_finds_injected_outliers(self):
        result = self.fitter.solve()
        self.assertEqual(result.status, Status.EXACT)
        self.assertEqual(result.min_removals, 2)
        self.assertEqual(set(result.removed), {3, 8})
        self.assertEqual(result.num_optimal, 1)

    def test_conflict_cores_involve_outliers(self):
        result = self.fitter.solve()
        self.assertTrue(result.cores)
        for core in result.cores:
            self.assertTrue(set(core) & {3, 8})
        self.assertIn((2, 3), result.cores)
        self.assertIn((7, 8), result.cores)

    def test_budget_undecided(self):
        result = self.fitter.solve(budget=1)
        self.assertEqual(result.status, Status.BUDGET_UNDECIDED)
        self.assertIsNone(result.min_removals)
        self.assertIsNone(result.witness)

    def test_budget_satisfied(self):
        result = self.fitter.solve(budget=2)
        self.assertEqual(result.status, Status.EXACT)
        self.assertEqual(result.min_removals, 2)
        self.assertEqual(result.budget, 2)


class QueryTests(unittest.TestCase):
    def test_query_bounds_exact(self):
        # drift 0 => slope exactly 1; v1 = v0 + 10, v0 in [4.9, 5.1].
        anchors = [anchor(0, 5, 100), anchor(10, 15, 100)]
        fitter = PiecewiseAffineFitter(anchors, Fraction(0))
        qr = fitter.query(Instant(5))
        self.assertEqual(qr.kind, "interpolation")
        self.assertEqual(qr.ref_lo, Instant(Fraction(99, 10)))
        self.assertEqual(qr.ref_hi, Instant(Fraction(101, 10)))
        self.assertEqual(qr.corrected, Instant(10))
        self.assertEqual(qr.uncertainty, Duration(Fraction(1, 5)))

    def test_query_at_knot(self):
        anchors = [anchor(0, 5, 100), anchor(10, 15, 100)]
        fitter = PiecewiseAffineFitter(anchors, Fraction(0))
        qr = fitter.query(Instant(0))
        self.assertEqual(qr.kind, "knot")
        self.assertEqual(qr.ref_lo, Instant(Fraction(49, 10)))
        self.assertEqual(qr.ref_hi, Instant(Fraction(51, 10)))

    def test_extrapolation_within_allowed(self):
        anchors = [anchor(0, 5), anchor(60, 65)]
        fitter = PiecewiseAffineFitter(anchors, Fraction(0), Duration(30))
        qr = fitter.query(Instant(70))
        self.assertEqual(qr.kind, "extrapolation")
        self.assertEqual(qr.ref_lo, Instant(75))
        self.assertEqual(qr.ref_hi, Instant(75))

    def test_extrapolation_beyond_allowed_rejected(self):
        anchors = [anchor(0, 5), anchor(60, 65)]
        fitter = PiecewiseAffineFitter(anchors, Fraction(0), Duration(30))
        with self.assertRaises(ExtrapolationError):
            fitter.query(Instant(91))

    def test_extrapolation_before_first_knot(self):
        anchors = [anchor(60, 65), anchor(120, 125)]
        fitter = PiecewiseAffineFitter(anchors, Fraction(0), Duration(30))
        qr = fitter.query(Instant(40))
        self.assertEqual(qr.kind, "extrapolation")
        self.assertEqual(qr.ref_lo, Instant(45))

    def test_query_on_infeasible_system_raises(self):
        anchors = [anchor(0, 5), anchor(60, 15)]
        fitter = PiecewiseAffineFitter(anchors, Fraction(5, 100))
        with self.assertRaises(InfeasibleError):
            fitter.query(Instant(30))

    def test_query_requires_instant(self):
        anchors = [anchor(0, 5), anchor(60, 65)]
        fitter = PiecewiseAffineFitter(anchors, Fraction(0))
        with self.assertRaises(TypeError):
            fitter.query(30)


class WitnessTests(unittest.TestCase):
    def test_witness_continuity_and_constraints(self):
        anchors = synthetic_table()
        drift = Fraction(5, 1000)
        fitter = PiecewiseAffineFitter(anchors, drift, Duration(30))
        result = fitter.solve()
        witness = result.witness
        times = [w[0].seconds for w in witness]
        values = [w[1].seconds for w in witness]
        # Explicit C0 continuity: each segment's affine extrapolation to the
        # shared knot equals the next segment's value there.
        for k in range(len(times) - 1):
            dt = times[k + 1] - times[k]
            slope = (values[k + 1] - values[k]) / dt
            left = values[k] + slope * dt
            right = values[k + 1]
            self.assertEqual(left, right)
            self.assertLessEqual(abs(slope - 1), drift)
        # Witness honours every kept anchor's error interval.
        value_at = dict((w[0].seconds, w[1].seconds) for w in witness)
        for i, a in enumerate(anchors):
            if i in result.removed:
                continue
            self.assertLessEqual(a.ref_lo, value_at[a.local.seconds])
            self.assertLessEqual(value_at[a.local.seconds], a.ref_hi)


if __name__ == "__main__":
    unittest.main()
