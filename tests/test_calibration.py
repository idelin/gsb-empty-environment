import unittest
from fractions import Fraction

from timeproj.blocks import SamplingBlock
from timeproj.calibration import (
    CalibratedProjector,
    CalibrationStore,
    OffsetTable,
)
from timeproj.projection import project
from timeproj.types import Interval, TimePoint, WallClockTime


def tp(v):
    return TimePoint(Fraction(v))


class TestOffsetTable(unittest.TestCase):
    def test_exact_piecewise_linear_interpolation(self):
        t = OffsetTable([(0, 0), (10, 1)])
        self.assertEqual(t.offset_at(5), Fraction(1, 2))
        self.assertEqual(t.correct(5), Fraction(11, 2))
        self.assertEqual(t.uncorrect(Fraction(11, 2)), Fraction(5))
        self.assertEqual(t.offset_at(-3), Fraction(0))  # clamped before first
        self.assertEqual(t.offset_at(50), Fraction(1))  # clamped after last

    def test_uncorrect_roundtrip_on_nodes_and_between(self):
        t = OffsetTable([(0, 0), (10, 1), (20, 3)])
        for raw in [Fraction(0), Fraction(3), Fraction(10), Fraction(17),
                    Fraction(20)]:
            self.assertEqual(t.uncorrect(t.correct(raw)), raw)

    def test_monotonicity_is_enforced(self):
        with self.assertRaises(ValueError):
            OffsetTable([(0, 0), (10, -20)])  # corrected time goes backwards
        with self.assertRaises(ValueError):
            OffsetTable([(5, 0), (5, 1)])     # duplicate raw reading


class TestCalibratedProjection(unittest.TestCase):
    def setUp(self):
        self.a = SamplingBlock(0, 0, tp(0), Fraction(1), tp(0), tp(10))
        self.b = SamplingBlock(1, 0, tp(12), Fraction(1), tp(12), tp(100))
        self.blocks = [self.a, self.b]
        self.raw_query = Interval(tp(0), tp(20))  # [0, 20]

    def make_store(self):
        return CalibrationStore(OffsetTable([(0, 0), (1000, 0)], version=0))

    def bump_correction(self, store):
        # +1s offset for raw times beyond ~10, changed only inside (10, 11)
        store.update(
            OffsetTable([(0, 0), (10, 0), (11, 1), (1000, 1)], version=1),
            raw_span=(10, 11))

    def test_local_correction_recomputes_only_intersecting_slices(self):
        store = self.make_store()
        proj = CalibratedProjector(store, self.blocks)
        r0 = proj.project(self.raw_query)
        entries0 = dict(proj._states[self.raw_query].entries)

        self.bump_correction(store)
        r1 = proj.project(self.raw_query)
        entries1 = proj._states[self.raw_query].entries

        # block A covers [0,10): untouched by the (10,11) correction
        self.assertIs(entries1[(0, 0)], entries0[(0, 0)])
        # block B covers [12,100): intersects the moved query end
        self.assertIsNot(entries1[(1, 0)], entries0[(1, 0)])

        # B now reaches corrected t=21, A is unchanged
        got = {(s.epoch, s.version): (s.start_index, s.end_index)
               for s in r1.slices}
        self.assertEqual(got[(0, 0)], (0, 10))
        self.assertEqual(got[(1, 0)], (0, 10))
        self.assertEqual(r1.query.end.value, Fraction(21))

        # partial reuse must equal a fresh full projection on the new table
        fresh = project(Interval(tp(0), tp(21)), self.blocks)
        self.assertEqual(r1.slices, fresh.slices)
        self.assertEqual(r1.gaps, fresh.gaps)
        self.assertEqual(r1.missing, fresh.missing)
        self.assertEqual(r1.dedupe, fresh.dedupe)

    def test_cursor_keeps_historical_version(self):
        store = self.make_store()
        proj = CalibratedProjector(store, self.blocks)
        r0 = proj.project(self.raw_query)
        cursor = proj.open_cursor()  # bound to version 0

        self.bump_correction(store)
        r1 = proj.project(self.raw_query)
        rc = cursor.project(self.raw_query)

        self.assertEqual(cursor.version, 0)
        self.assertEqual(rc.query.end.value, Fraction(20))  # historical view
        self.assertEqual(r1.query.end.value, Fraction(21))  # updated view
        self.assertEqual(rc.slices, r0.slices)
        self.assertNotEqual(r1.slices, r0.slices)

    def test_wall_clock_query_and_clock_mismatch(self):
        store = self.make_store()
        proj = CalibratedProjector(store, self.blocks)
        r = proj.project_raw(WallClockTime(0, "wall"), WallClockTime(20, "wall"))
        self.assertTrue(r.slices)
        with self.assertRaises(TypeError):
            proj.project_raw(WallClockTime(0, "gps"), WallClockTime(20, "wall"))

    def test_gap_survives_calibration_and_is_never_zero_filled(self):
        store = self.make_store()
        proj = CalibratedProjector(store, self.blocks)
        r = proj.project(self.raw_query)
        self.assertEqual(len(r.gaps), 1)
        self.assertEqual((r.gaps[0].interval.start.value,
                          r.gaps[0].interval.end.value),
                         (Fraction(10), Fraction(12)))
        self.assertEqual(r.sample_count, 10 + 9)  # no fake zeros anywhere


if __name__ == "__main__":
    unittest.main()
