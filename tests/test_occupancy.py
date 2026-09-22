"""Automated tests: pairing, index updates, peak queries, brute-force cross-check."""

import os
import random
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from occupancy import OccupancyTracker, Result


def brute_peak(intervals, lo, hi):
    """Reference: per-endpoint scan over every elementary segment.

    Splits [lo, hi) at every clipped endpoint and counts, for each elementary
    segment, the intervals covering its midpoint. Returns (peak, periods)
    where periods are maximal runs merged only when the contributing id set
    is identical -- mirroring OccupancyTracker.peak's contract.
    """
    pts = {lo, hi}
    for s, e in intervals.values():
        if s < hi and e > lo:
            pts.add(max(s, lo))
            pts.add(min(e, hi))
    pts = sorted(pts)
    best, periods = 0, []
    for a, b in zip(pts, pts[1:]):
        mid = (a + b) / 2
        ids = frozenset(oid for oid, (s, e) in intervals.items() if s <= mid < e)
        c = len(ids)
        if c == 0:
            continue
        if c > best:
            best, periods = c, []
        if c == best:
            if periods and periods[-1][1] == a and periods[-1][2] == ids:
                periods[-1] = (periods[-1][0], b, ids)
            else:
                periods.append((a, b, ids))
    return best, periods


def as_tuples(peak_result):
    return peak_result.peak, [(p.start, p.end, p.occupancy_ids) for p in peak_result.periods]


def check_index_consistency(test, tr):
    """The delta index must exactly match a rebuild from active intervals."""
    delta = {}
    for s, e in tr.intervals().values():
        delta[s] = delta.get(s, 0) + 1
        delta[e] = delta.get(e, 0) - 1
    test.assertEqual(delta, tr._index._delta)
    # every active interval is registered in the per-endpoint event maps
    for oid, (s, e) in tr.intervals().items():
        test.assertIn(oid, tr._by_start[s])
        test.assertIn(oid, tr._by_end[e])


class TestPairing(unittest.TestCase):
    def test_end_arriving_before_start_pairs(self):
        tr = OccupancyTracker()
        self.assertIs(tr.record_end("A", 5), Result.RECORDED)
        self.assertIs(tr.record_start("A", 0), Result.PAIRED)
        self.assertEqual(tr.intervals(), {"A": (0, 5)})
        self.assertEqual(tr.orphans(), {})

    def test_orphan_listed_separately_no_phantom_interval(self):
        tr = OccupancyTracker()
        tr.record_start("E", 2)
        self.assertEqual(tr.intervals(), {})
        self.assertEqual(tr.orphans()["E"]["status"], "waiting_end")
        r = tr.peak(0, 10)
        self.assertEqual(r.peak, 0)  # no zero-padded fake occupancy
        self.assertEqual(r.periods, ())

    def test_invalid_pair_stored_but_not_indexed(self):
        tr = OccupancyTracker()
        tr.record_start("D", 3)
        self.assertIs(tr.record_end("D", 1), Result.INVALID_PAIR)
        self.assertEqual(tr.intervals(), {})
        self.assertEqual(tr.orphans()["D"]["status"], "invalid")
        self.assertEqual(tr.peak(0, 10).peak, 0)

    def test_zero_length_interval_is_invalid(self):
        tr = OccupancyTracker()
        tr.record_start("Z", 4)
        self.assertIs(tr.record_end("Z", 4), Result.INVALID_PAIR)
        self.assertEqual(tr.intervals(), {})

    def test_invalid_pair_recovers_via_correction(self):
        tr = OccupancyTracker()
        tr.record_start("D", 3)
        tr.record_end("D", 1)
        self.assertIs(tr.correct_end("D", 8), Result.CORRECTED)
        self.assertEqual(tr.intervals(), {"D": (3, 8)})

    def test_duplicate_and_conflicting_end(self):
        tr = OccupancyTracker()
        tr.record_start("A", 0)
        tr.record_end("A", 5)
        self.assertIs(tr.record_end("A", 5), Result.DUPLICATE)
        self.assertIs(tr.record_end("A", 7), Result.CONFLICT)
        self.assertEqual(tr.intervals(), {"A": (0, 5)})  # unchanged


class TestPeakQuery(unittest.TestCase):
    def setUp(self):
        self.tr = OccupancyTracker()
        self.tr.record_start("A", 0)
        self.tr.record_end("A", 5)
        self.tr.record_start("B", 5)
        self.tr.record_end("B", 9)

    def test_touching_intervals_do_not_overlap(self):
        r = self.tr.peak(0, 10)
        self.assertEqual(r.peak, 1)
        self.assertEqual(
            as_tuples(r)[1],
            [(0.0, 5.0, frozenset({"A"})), (5.0, 9.0, frozenset({"B"}))],
        )

    def test_overlap_raises_peak_and_retract_restores(self):
        self.tr.record_start("C", 4)
        self.tr.record_end("C", 6)
        r = self.tr.peak(0, 10)
        self.assertEqual(r.peak, 2)
        self.assertEqual(
            as_tuples(r)[1],
            [(4.0, 5.0, frozenset({"A", "C"})), (5.0, 6.0, frozenset({"B", "C"}))],
        )
        self.assertIs(self.tr.retract("C"), Result.RETRACTED)
        r = self.tr.peak(0, 10)
        self.assertEqual(r.peak, 1)
        self.assertEqual(r.periods[0].occupancy_ids, frozenset({"A"}))

    def test_query_subrange_clips_periods(self):
        r = self.tr.peak(2, 7)
        self.assertEqual(as_tuples(r)[1], [(2.0, 5.0, frozenset({"A"})),
                                           (5.0, 7.0, frozenset({"B"}))])

    def test_empty_range_rejected(self):
        with self.assertRaises(ValueError):
            self.tr.peak(5, 5)


class TestCorrectionsAndInvalidEnd(unittest.TestCase):
    def test_correction_moving_out_of_range_revokes_old_contribution(self):
        tr = OccupancyTracker()
        tr.record_start("A", 0)
        tr.record_end("A", 5)
        tr.record_start("B", 5)
        tr.record_end("B", 9)
        self.assertIs(tr.correct_end("B", 25), Result.CORRECTED)
        self.assertIs(tr.correct_start("B", 20), Result.CORRECTED)
        r = tr.peak(0, 10)
        self.assertEqual(r.peak, 1)
        for p in r.periods:
            self.assertNotIn("B", p.occupancy_ids)  # old [5,9) contribution gone
        self.assertEqual(tr.count_at(6), 0)
        check_index_consistency(self, tr)

    def test_invalid_end_correction_rejected_state_untouched(self):
        tr = OccupancyTracker()
        tr.record_start("B", 2)
        tr.record_end("B", 5)
        before = tr.intervals().copy()
        self.assertIs(tr.correct_end("B", 1), Result.INVALID)
        self.assertIs(tr.correct_start("B", 9), Result.INVALID)
        self.assertEqual(tr.intervals(), before)
        self.assertEqual(tr.peak(0, 10).peak, 1)
        check_index_consistency(self, tr)

    def test_retract_unknown_id(self):
        tr = OccupancyTracker()
        self.assertIs(tr.retract("ghost"), Result.NOT_FOUND)
        self.assertIs(tr.correct_start("ghost", 1), Result.NOT_FOUND)


class TestRandomizedCrossCheck(unittest.TestCase):
    def test_random_ops_match_brute_force(self):
        rng = random.Random(20260922)
        tr = OccupancyTracker()
        ids = [f"occ{i}" for i in range(8)]
        for _ in range(300):
            oid = rng.choice(ids)
            t = rng.randint(0, 30)
            op = rng.random()
            if op < 0.35:
                tr.record_start(oid, t)
            elif op < 0.65:
                tr.record_end(oid, t)
            elif op < 0.8:
                tr.correct_start(oid, t)
            elif op < 0.95:
                tr.correct_end(oid, t)
            else:
                tr.retract(oid)
        check_index_consistency(self, tr)
        intervals = tr.intervals()
        for _ in range(50):
            a, b = sorted((rng.randint(0, 30), rng.randint(0, 30)))
            if a == b:
                b += 1
            expected = brute_peak(intervals, float(a), float(b))
            got = as_tuples(tr.peak(a, b))
            self.assertEqual(expected, got, f"range [{a}, {b}) intervals={intervals}")
        # unbounded query agrees as well
        expected = brute_peak(intervals, -1.0, 40.0)
        got = as_tuples(tr.peak())
        got = (got[0], [(max(s, -1.0), min(e, 40.0), ids) for s, e, ids in got[1]])
        self.assertEqual(expected, got)


if __name__ == "__main__":
    unittest.main()
