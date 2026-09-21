"""Tests for the two-level time aggregation index.

A plain direct scan over the event store serves as the independent
reference for every query assertion.
"""

import random
import unittest

from time_index import TwoLevelTimeIndex


def reference_sum(events, lo, hi):
    """Independent reference: brute-force scan, no index involved."""
    return sum(v for t, v in events.values() if lo <= t < hi)


class TwoLevelQueryTest(unittest.TestCase):
    def setUp(self):
        self.index = TwoLevelTimeIndex(fine_width=5, coarse_width=20)
        # (id, time, value); covers edges, full fine buckets and a coarse
        # bucket that is only partially filled at the fine level.
        samples = [
            (1, 3, 10),    # head edge of [3, 23)
            (2, 4, 20),    # head edge of [3, 23)
            (3, 7, 5),     # fine bucket 1
            (4, 12, 6),    # fine bucket 2
            (5, 19, 7),    # fine bucket 3
            (6, 21, 8),    # tail edge of [3, 23)
            (7, 22, 9),    # tail edge of [3, 23)
            (8, 30, 100),  # outside the query range
        ]
        self.events = {}
        for event_id, t, value in samples:
            self.index.add(event_id, t, value)
            self.events[event_id] = (t, value)

    def test_query_3_23_uses_both_levels(self):
        result = self.index.query_with_stats(3, 23)
        self.assertEqual(result.total, reference_sum(self.events, 3, 23))
        self.assertEqual(result.total, 10 + 20 + 5 + 6 + 7 + 8 + 9)
        # Fine buckets 1, 2, 3 ([5,20)) are full -> 3 aggregate nodes;
        # no coarse bucket fits inside [3, 23).
        self.assertEqual(result.agg_nodes, 3)
        # Edge slivers [3,5) and [20,23) hold events 1, 2 and 6, 7.
        self.assertEqual(result.edge_events, 4)

    def test_move_boundary_event_out_of_range(self):
        before = self.index.query(3, 23)
        # Event 2 sits at t=4, inside the head edge sliver; move it away.
        self.index.update(2, 40, 20)
        self.events[2] = (40, 20)

        # Raw record revised.
        self.assertEqual(self.index.get(2), (40, 20))
        # Both aggregate levels revised: fine bucket 0 lost 20, coarse
        # bucket 0 lost 20, coarse bucket 2 ([40,60)) gained 20.
        self.assertEqual(self.index._fine[0], 10)     # only event 1 (t=3)
        self.assertEqual(self.index._coarse[0], 10 + 5 + 6 + 7)
        self.assertEqual(self.index._coarse[2], 20)
        # Query sees the revised state, matching the reference scan.
        self.assertEqual(self.index.query(3, 23), before - 20)
        self.assertEqual(self.index.query(3, 23),
                         reference_sum(self.events, 3, 23))

    def test_correct_value_in_place(self):
        self.index.update(4, 12, 60)  # same timestamp, new value
        self.events[4] = (12, 60)
        self.assertEqual(self.index._fine[2], 60)
        self.assertEqual(self.index._coarse[0], 10 + 20 + 5 + 60 + 7)
        self.assertEqual(self.index.query(3, 23),
                         reference_sum(self.events, 3, 23))

    def test_retraction_unwinds_both_levels(self):
        self.index.remove(3)  # t=7, value 5
        del self.events[3]
        self.assertNotIn(1, self.index._fine)  # bucket became empty
        self.assertEqual(self.index._coarse[0], 10 + 20 + 6 + 7)
        self.assertEqual(self.index.query(3, 23),
                         reference_sum(self.events, 3, 23))
        self.index.remove(1)
        self.index.remove(2)
        self.assertNotIn(0, self.index._fine)  # fine bucket 0 empty now
        self.assertEqual(self.index._coarse[0], 6 + 7)  # events 4, 5 remain

    def test_failed_mutation_leaves_index_untouched(self):
        snapshot_fine = dict(self.index._fine)
        snapshot_coarse = dict(self.index._coarse)
        with self.assertRaises(KeyError):
            self.index.update(999, 0, 1)
        with self.assertRaises(KeyError):
            self.index.remove(999)
        with self.assertRaises(KeyError):
            self.index.add(1, 0, 1)  # duplicate id
        self.assertEqual(self.index._fine, snapshot_fine)
        self.assertEqual(self.index._coarse, snapshot_coarse)
        self.assertEqual(self.index.query(3, 23),
                         reference_sum(self.events, 3, 23))


class EdgeCaseTest(unittest.TestCase):
    def setUp(self):
        self.index = TwoLevelTimeIndex(fine_width=5, coarse_width=20)
        self.events = {}

    def _add(self, event_id, t, value):
        self.index.add(event_id, t, value)
        self.events[event_id] = (t, value)

    def test_empty_range(self):
        self._add(1, 10, 5)
        for lo, hi in [(10, 10), (15, 10), (0, 0), (-5, -5)]:
            result = self.index.query_with_stats(lo, hi)
            self.assertEqual(result.total, 0)
            self.assertEqual(result.agg_nodes, 0)
            self.assertEqual(result.edge_events, 0)

    def test_exactly_whole_windows(self):
        for i, t in enumerate(range(0, 40)):
            self._add(i, t, 1)
        # Aligned to the coarse grid: only coarse nodes, no edge events.
        result = self.index.query_with_stats(0, 40)
        self.assertEqual(result.total, 40)
        self.assertEqual(result.agg_nodes, 2)
        self.assertEqual(result.edge_events, 0)
        # Aligned to the fine grid only: only fine nodes.
        result = self.index.query_with_stats(5, 20)
        self.assertEqual(result.total, 15)
        self.assertEqual(result.agg_nodes, 3)
        self.assertEqual(result.edge_events, 0)
        # Single exact fine bucket.
        result = self.index.query_with_stats(10, 15)
        self.assertEqual(result.total, 5)
        self.assertEqual(result.agg_nodes, 1)
        self.assertEqual(result.edge_events, 0)

    def test_negative_times(self):
        self._add(1, -23, 3)   # fine bucket -5 ([-25,-20)), coarse -2
        self._add(2, -11, 5)   # fine bucket -3, coarse -1
        self._add(3, -7, 7)    # fine bucket -2, coarse -1
        self._add(4, -1, 11)   # fine bucket -1, coarse -1
        self._add(5, 0, 13)    # fine bucket 0, coarse 0
        for lo, hi in [(-24, 0), (-23, 3), (-25, 5), (-8, -1),
                       (-20, -20), (-100, 100), (-7, 0)]:
            self.assertEqual(self.index.query(lo, hi),
                             reference_sum(self.events, lo, hi),
                             msg=f"range [{lo}, {hi})")
        # Floor-division bucket placement for negatives.
        self.assertEqual(self.index._fine[-5], 3)
        self.assertEqual(self.index._coarse[-2], 3)
        self.assertEqual(self.index._coarse[-1], 5 + 7 + 11)
        # Fully covered negative coarse bucket [-20, 0).
        result = self.index.query_with_stats(-20, 0)
        self.assertEqual(result.total, 5 + 7 + 11)
        self.assertEqual(result.agg_nodes, 1)
        self.assertEqual(result.edge_events, 0)

    def test_coarse_window_with_empty_fine_windows(self):
        # Coarse bucket 1 ([20,40)) with only fine buckets 4 and 7 filled.
        self._add(1, 21, 2)   # fine bucket 4
        self._add(2, 24, 3)   # fine bucket 4
        self._add(3, 39, 5)   # fine bucket 7
        self.assertEqual(self.index._coarse[1], 10)
        self.assertNotIn(5, self.index._fine)
        self.assertNotIn(6, self.index._fine)
        # Whole coarse bucket reads one coarse node despite sparse fines.
        result = self.index.query_with_stats(20, 40)
        self.assertEqual(result.total, 10)
        self.assertEqual(result.agg_nodes, 1)
        self.assertEqual(result.edge_events, 0)
        # Sub-range crossing the empty fine windows.
        for lo, hi in [(22, 38), (25, 35), (20, 36), (26, 39), (0, 39)]:
            self.assertEqual(self.index.query(lo, hi),
                             reference_sum(self.events, lo, hi),
                             msg=f"range [{lo}, {hi})")
        # [25, 35) covers only empty fine buckets 5 and 6.
        result = self.index.query_with_stats(25, 35)
        self.assertEqual(result.total, 0)
        self.assertEqual(result.agg_nodes, 0)
        self.assertEqual(result.edge_events, 0)


class RandomizedCrossCheckTest(unittest.TestCase):
    def test_random_ops_against_reference(self):
        rng = random.Random(20260921)
        index = TwoLevelTimeIndex(fine_width=5, coarse_width=20)
        events = {}
        next_id = 0
        for _ in range(2000):
            op = rng.random()
            if op < 0.45 or not events:
                t = rng.randint(-150, 150)
                v = rng.randint(1, 50)
                index.add(next_id, t, v)
                events[next_id] = (t, v)
                next_id += 1
            elif op < 0.75:
                event_id = rng.choice(list(events))
                t = rng.randint(-150, 150)
                v = rng.randint(1, 50)
                index.update(event_id, t, v)
                events[event_id] = (t, v)
            else:
                event_id = rng.choice(list(events))
                index.remove(event_id)
                del events[event_id]
            if rng.random() < 0.4:
                lo = rng.randint(-160, 160)
                hi = rng.randint(-160, 160)
                self.assertEqual(index.query(lo, hi),
                                 reference_sum(events, lo, hi))
        # Final sweep of structured ranges, including aligned ones.
        for lo in range(-160, 161, 20):
            for hi in range(lo, 161, 20):
                self.assertEqual(index.query(lo, hi),
                                 reference_sum(events, lo, hi))


class WideSpanReportTest(unittest.TestCase):
    def test_many_full_buckets_report(self):
        index = TwoLevelTimeIndex(fine_width=5, coarse_width=20)
        events = {}
        # One event per timestamp across [-1000, 1000).
        for t in range(-1000, 1000):
            index.add(t + 1000, t, 1)
            events[t + 1000] = (t, 1)

        lo, hi = 3, 203
        result = index.query_with_stats(lo, hi)
        self.assertEqual(result.total, reference_sum(events, lo, hi))
        self.assertEqual(result.total, hi - lo)
        # Coarse buckets 1..9 ([20,200)) are full: 9 coarse nodes.
        # Head [3,20): fine buckets 1..3 full (3 fine nodes) + edge [3,5).
        # Tail [200,203): no full fine bucket, edge [200,203).
        self.assertEqual(result.agg_nodes, 9 + 3)
        self.assertEqual(result.edge_events, 2 + 3)
        print(f"\n[report] query [{lo}, {hi}): total={result.total}, "
              f"aggregate nodes read={result.agg_nodes}, "
              f"edge raw events read={result.edge_events}")

        # A much wider span: edges stay tiny while aggregates scale.
        lo, hi = -997, 998
        result = index.query_with_stats(lo, hi)
        self.assertEqual(result.total, reference_sum(events, lo, hi))
        # Coarse buckets -49..48 fully inside ([-980, 980)): 98 nodes,
        # plus 3 fine nodes per head/tail, plus small edge slivers.
        self.assertEqual(result.agg_nodes, 98 + 3 + 3)
        self.assertEqual(result.edge_events, 2 + 3)
        print(f"[report] query [{lo}, {hi}): total={result.total}, "
              f"aggregate nodes read={result.agg_nodes}, "
              f"edge raw events read={result.edge_events}")


if __name__ == "__main__":
    unittest.main(verbosity=2)
