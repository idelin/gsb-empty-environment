import unittest
from fractions import Fraction

from timeproj.blocks import SamplingBlock
from timeproj.projection import project
from timeproj.types import Interval, TimePoint


def tp(v):
    return TimePoint(Fraction(v))


def query(lo, hi, start_open=False, end_open=False):
    return Interval(tp(lo), tp(hi), start_open, end_open)


def block(epoch=0, version=0, origin=0, rate=1, start=0, end=10, missing=()):
    return SamplingBlock(epoch, version, tp(origin), Fraction(rate),
                         tp(start), None if end is None else tp(end),
                         frozenset(missing))


def spans(result):
    got = {}
    for s in result.slices:
        got.setdefault(s.key, []).append((s.start_index, s.end_index))
    return got


class TestOpenClosedEndpoints(unittest.TestCase):
    def test_closed_interval_contains_two_points(self):
        r = project(query(0, 1), [block(rate=1)])
        self.assertEqual(spans(r), {(0, 0): [(0, 2)]})  # t=0 and t=1
        self.assertEqual(r.sample_count, 2)

    def test_half_open_interval_contains_one_point(self):
        r = project(query(0, 1, end_open=True), [block(rate=1)])
        self.assertEqual(spans(r), {(0, 0): [(0, 1)]})  # only t=0
        self.assertEqual(r.sample_count, 1)

    def test_open_start_excludes_boundary_sample(self):
        r = project(query(0, 1, start_open=True), [block(rate=1)])
        self.assertEqual(spans(r), {(0, 0): [(1, 2)]})  # only t=1


class TestNegativeTimes(unittest.TestCase):
    def test_ceil_floor_are_correct_below_zero(self):
        b = block(rate=2, start=-10, end=10)  # period 1/2
        r = project(query(Fraction(-13, 10), Fraction(-1, 5)), [b])
        self.assertEqual(spans(r), {(0, 0): [(-2, 0)]})
        stamps = [b.timestamp(k).value for k in range(-2, 0)]
        self.assertEqual(stamps, [Fraction(-1), Fraction(-1, 2)])

    def test_open_interval_between_grid_points_is_empty(self):
        b = block(rate=2, start=-10, end=10)
        r = project(query(-1, Fraction(-1, 2), start_open=True, end_open=True),
                    [b])
        self.assertEqual(r.slices, [])
        self.assertEqual(r.gaps, [])  # covered region, just no sample inside
        self.assertEqual(r.sample_count, 0)

    def test_closed_negative_boundary_hits(self):
        b = block(rate=2, start=-10, end=10)
        r = project(query(-1, Fraction(-1, 2)), [b])
        self.assertEqual(spans(r), {(0, 0): [(-2, 0)]})  # t=-1 and t=-1/2


class TestRationalRate(unittest.TestCase):
    def test_rate_three_halves(self):
        b = block(rate=Fraction(3, 2))  # period 2/3
        r = project(query(0, 2), [b])
        self.assertEqual(spans(r), {(0, 0): [(0, 4)]})
        stamps = [b.timestamp(k).value for k in range(4)]
        self.assertEqual(stamps, [Fraction(0), Fraction(2, 3),
                                  Fraction(4, 3), Fraction(2)])


class TestMissingSamplesAndGaps(unittest.TestCase):
    def test_missing_sample_splits_slice_and_is_reported(self):
        b = block(missing={2})
        r = project(query(0, 4), [b])
        self.assertEqual(spans(r), {(0, 0): [(0, 2), (3, 5)]})
        self.assertEqual([m.index for m in r.missing], [2])
        self.assertEqual(r.missing[0].timestamp.value, Fraction(2))
        self.assertEqual(r.gaps, [])  # a dropped sample is not a coverage gap

    def test_gap_between_blocks_is_explicit_not_zero_filled(self):
        a = block(epoch=0, start=0, end=10)
        b = block(epoch=1, origin=12, start=12, end=20)
        r = project(query(0, 20, end_open=True), [a, b])
        self.assertEqual(len(r.gaps), 1)
        self.assertEqual(r.gaps[0].reason, "no-coverage")
        gap = r.gaps[0].interval
        self.assertEqual((gap.start.value, gap.end.value),
                         (Fraction(10), Fraction(12)))
        # 10 + 8 real samples; a fake contiguous array would claim 20
        self.assertEqual(r.sample_count, 18)


class TestOverlapDedupe(unittest.TestCase):
    def test_later_epoch_wins_at_identical_timestamps(self):
        a = SamplingBlock(0, 0, tp(0), Fraction(1), tp(0), tp(10))
        c = SamplingBlock(2, 1, tp(5), Fraction(1), tp(5), tp(8))
        r = project(query(0, 10, end_open=True), [a, c])
        self.assertEqual(spans(r), {(2, 1): [(0, 3)],
                                    (0, 0): [(0, 5), (8, 10)]})
        self.assertEqual(len(r.dedupe), 3)
        self.assertTrue(all(e.kept[:2] == (2, 1) for e in r.dedupe))
        self.assertTrue(all(e.dropped[:2] == (0, 0) for e in r.dedupe))
        self.assertEqual(r.sample_count, 10)  # t=0..9 exactly once

    def test_higher_version_wins_within_same_epoch(self):
        old = SamplingBlock(0, 0, tp(0), Fraction(1), tp(0), tp(10))
        new = SamplingBlock(0, 1, tp(2), Fraction(1), tp(2), tp(5))
        r = project(query(0, 6, end_open=True), [old, new])
        self.assertEqual(spans(r), {(0, 1): [(0, 3)],
                                    (0, 0): [(0, 2), (5, 6)]})
        self.assertTrue(all(e.kept[:2] == (0, 1) for e in r.dedupe))

    def test_cross_rate_coincidence_uses_exact_diophantine(self):
        # period 1 vs period 3/2: grids coincide every 3 seconds
        a = SamplingBlock(0, 0, tp(0), Fraction(1), tp(0), tp(10))
        b = SamplingBlock(1, 0, tp(0), Fraction(2, 3), tp(0), tp(10))
        r = project(query(0, 10, end_open=True), [a, b])
        self.assertEqual({e.timestamp.value for e in r.dedupe},
                         {Fraction(0), Fraction(3), Fraction(6), Fraction(9)})
        self.assertTrue(all(e.kept[:2] == (1, 0) for e in r.dedupe))
        self.assertEqual(spans(r)[(0, 0)], [(1, 3), (4, 6), (7, 9)])


class TestReverseAndEvidence(unittest.TestCase):
    def test_reverse_gives_actual_covered_interval(self):
        b = block(rate=1)
        r = project(query(Fraction(1, 2), 2), [b])
        self.assertEqual(spans(r), {(0, 0): [(1, 3)]})
        cov = r.reverse()[0]
        self.assertEqual(cov.interval.start.value, Fraction(1))
        self.assertEqual(cov.interval.end.value, Fraction(2))
        self.assertFalse(cov.interval.start_open)
        self.assertFalse(cov.interval.end_open)
        # Voronoi cell extends half a period beyond the end samples
        self.assertEqual(cov.cell.start.value, Fraction(1, 2))
        self.assertEqual(cov.cell.end.value, Fraction(5, 2))
        self.assertTrue(cov.cell.end_open)

    def test_rounding_evidence_records_how_endpoints_were_rounded(self):
        b = block(rate=1)
        r = project(query(Fraction(1, 2), 2), [b])
        ev = {e.endpoint: e for e in r.evidence}
        self.assertFalse(ev["start"].exact_hit)
        self.assertEqual(ev["start"].raw_value, Fraction(1, 2))
        self.assertEqual(ev["start"].direction, "ceil")
        self.assertEqual(ev["start"].index, 1)
        self.assertEqual(ev["start"].timestamp.value, Fraction(1))
        self.assertTrue(ev["end"].exact_hit)
        self.assertEqual(ev["end"].direction, "floor")
        self.assertEqual(ev["end"].index, 2)
        # reverse coverage carries the forward rounding evidence
        self.assertEqual(len(r.reverse()[0].evidence), 2)


if __name__ == "__main__":
    unittest.main()
