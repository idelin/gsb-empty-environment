"""Unittest suite for the layered event-count engine."""

import bisect
import math
import random
import unittest
from fractions import Fraction

import treap
from buckets import BucketSchema
from engine import EventEngine
from timemodel import Duration, OffsetTable, TimePoint, WallClock


def make_engine():
    schema = BucketSchema(fine_width=10, business_boundaries=(-20, 0, 20, 50),
                          version=0)
    return EventEngine(schema)


class TestFloorBuckets(unittest.TestCase):
    def test_negative_ticks_round_down(self):
        schema = BucketSchema(fine_width=10)
        self.assertEqual(schema.leaf_bucket(-1), -1)   # [-10, 0), not [0, 10)
        self.assertEqual(schema.leaf_bucket(-10), -1)
        self.assertEqual(schema.leaf_bucket(-11), -2)
        self.assertEqual(schema.leaf_bucket(0), 0)
        self.assertEqual(schema.leaf_bucket(9), 0)
        self.assertEqual(schema.bucket_span(-1), (-10, 0))

    def test_minus_one_counts_in_negative_bucket(self):
        engine = make_engine()
        engine.add("neg", -1)
        engine.add("zero", 0)
        total, contribs = engine.range_count(-10, 0)    # exactly bucket [-10, 0)
        self.assertEqual(total, 1)                      # only "neg"
        self.assertEqual((contribs[0].layer, contribs[0].count), ("fine", 1))
        total, _ = engine.range_count(0, 10)
        self.assertEqual(total, 1)                      # only "zero"

    def test_each_event_in_exactly_one_leaf_bucket(self):
        engine = make_engine()
        for i, t in enumerate([-25, -11, -10, -1, 0, 5, 10, 19, 20, 44]):
            engine.add(i, t)
        version = engine._version(None)
        bucket_total = treap.range_total(version.buckets)
        self.assertEqual(bucket_total, engine.size())
        for t, _seq, _eid in engine.items():
            idx = engine._schema_of(version).leaf_bucket(t)
            lo, hi = engine._schema_of(version).bucket_span(idx)
            self.assertTrue(lo <= t < hi)


class TestRangeCountLayers(unittest.TestCase):
    def setUp(self):
        self.engine = make_engine()
        self.times = [-25, -15, -10, -1, 0, 3, 3, 10, 17, 20, 25, 33, 49, 55]
        for i, t in enumerate(self.times):
            self.engine.add(f"e{i}", t)

    def brute(self, lo, hi):
        return sum(1 for t in self.times if lo <= t < hi)

    def test_exact_count_any_range(self):
        for lo in range(-30, 60, 3):
            for hi in range(lo + 1, 61, 4):
                total, _ = self.engine.range_count(lo, hi)
                self.assertEqual(total, self.brute(lo, hi), (lo, hi))

    def test_edges_accounted_item_by_item(self):
        # cuts through buckets [-20,-10), [0,10) and [40,50)
        total, contribs = self.engine.range_count(-15, 37)
        self.assertEqual(total, self.brute(-15, 37))
        edges = [c for c in contribs if c.layer == "edge"]
        self.assertEqual([(c.lo, c.hi) for c in edges], [(-15, -10), (30, 37)])
        edge_items = [item for c in edges for item in c.items]
        self.assertEqual([t for t, _s, _e in edge_items], [-15, 33])
        for c in edges:
            self.assertEqual(c.count, len(c.items))     # item-by-item

    def test_no_double_count_across_layers(self):
        total, contribs = self.engine.range_count(-15, 45)
        self.assertEqual(total, self.brute(-15, 45))
        self.assertEqual(sum(c.count for c in contribs), total)
        # spans are pairwise disjoint: nothing counted twice
        spans = sorted((c.lo, c.hi) for c in contribs)
        for (prev_lo, prev_hi), (next_lo, next_hi) in zip(spans, spans[1:]):
            self.assertLessEqual(prev_hi, next_lo)
        layers = {c.layer for c in contribs}
        self.assertEqual(layers, {"edge", "fine", "business"})
        business = [c for c in contribs if c.layer == "business"]
        self.assertEqual([(c.lo, c.hi) for c in business], [(0, 20)])
        self.assertEqual(business[0].count, 5)          # 0, 3, 3, 10, 17

    def test_contribution_provenance(self):
        snap = self.engine.snapshot()
        self.engine.add("late", 5)
        _total, contribs = self.engine.range_count(-30, 60, vid=snap)
        for c in contribs:
            self.assertEqual(c.data_version, snap)
            self.assertEqual(c.schema_version, 0)
            self.assertTrue(c.source)


class TestLateCorrectRetract(unittest.TestCase):
    def test_late_arrival(self):
        engine = make_engine()
        engine.add("a", 100)
        engine.add("b", 50)
        engine.add("late", -500)            # arrives after later events
        self.assertEqual(engine.range_count(-1000, 1000)[0], 3)
        self.assertEqual(engine.kth(1)[0], -500)

    def test_correction_moves_event_keeps_stable_order(self):
        engine = make_engine()
        engine.add("a", 5)
        engine.add("b", 5)                  # same time, later seq
        engine.correct("a", 7)
        self.assertEqual(engine.range_count(5, 8)[0], 2)
        self.assertEqual(engine.range_count(5, 6)[0], 1)   # only b left at 5
        engine.correct("a", 5)              # back to 5: a still sorts before b
        self.assertEqual(engine.kth(1)[1], "a")
        self.assertEqual(engine.kth(2)[1], "b")

    def test_retraction(self):
        engine = make_engine()
        engine.add("a", 5)
        engine.add("b", 15)
        engine.retract("a")
        self.assertEqual(engine.size(), 1)
        self.assertEqual(engine.range_count(0, 20)[0], 1)
        with self.assertRaises(KeyError):
            engine.retract("a")

    def test_old_snapshots_survive_updates(self):
        engine = make_engine()
        engine.add("a", 5)
        snap1 = engine.snapshot()
        engine.add("b", 6)
        snap2 = engine.snapshot()
        engine.retract("a")
        self.assertEqual(engine.range_count(0, 10, vid=snap1)[0], 1)
        self.assertEqual(engine.range_count(0, 10, vid=snap2)[0], 2)
        self.assertEqual(engine.range_count(0, 10)[0], 1)


class TestOrderStatistics(unittest.TestCase):
    def test_kth_with_duplicate_times_stable_by_seq(self):
        engine = make_engine()
        for eid in ["c", "a", "b"]:
            engine.add(eid, 42)             # identical times
        self.assertEqual([engine.kth(k)[1] for k in (1, 2, 3)],
                         ["c", "a", "b"])   # insertion (stable id) order

    def test_quantile(self):
        engine = make_engine()
        for i, t in enumerate([10, 20, 30, 40]):
            engine.add(i, t)
        self.assertEqual(engine.quantile(0.0), 10)
        self.assertEqual(engine.quantile(0.5), 20)
        self.assertEqual(engine.quantile(0.51), 30)
        self.assertEqual(engine.quantile(1.0), 40)
        with self.assertRaises(ValueError):
            engine.quantile(1.5)

    def test_kth_out_of_range(self):
        engine = make_engine()
        engine.add("a", 1)
        with self.assertRaises(IndexError):
            engine.kth(2)


class TestSchemaUpdate(unittest.TestCase):
    def test_old_snapshot_valid_after_schema_update(self):
        engine = make_engine()
        for eid, t in zip("abcd", [-15, -5, 5, 25]):
            engine.add(eid, t)
        snap = engine.snapshot()
        old_buckets_root = engine._version(snap).buckets
        before = engine.range_count(-20, 30, vid=snap)

        new_schema = BucketSchema(fine_width=5, business_boundaries=(-20, 5, 30),
                                  version=1)
        engine.update_schema(new_schema)
        engine.add("new", 6)

        after = engine.range_count(-20, 30, vid=snap)
        self.assertEqual(before, after)                  # snapshot intact
        self.assertIs(engine._version(snap).buckets, old_buckets_root)
        # head uses the new schema and keeps counting
        total, contribs = engine.range_count(-20, 30)
        self.assertEqual(total, 5)
        self.assertTrue(all(c.schema_version == 1 for c in contribs))
        business = [c for c in contribs if c.layer == "business"]
        self.assertEqual([(c.lo, c.hi) for c in business], [(-20, 5), (5, 30)])

    def test_incremental_updates_after_schema_change(self):
        engine = make_engine()
        engine.add("a", 3)
        engine.update_schema(BucketSchema(fine_width=2, version=1))
        for i in range(50):                              # no full rebuilds
            engine.add(f"n{i}", i)
        self.assertEqual(engine.range_count(0, 50)[0], 51)  # a@3 plus 0..49
        engine.retract("a")
        self.assertEqual(engine.range_count(0, 4)[0], 4)  # n0..n3 (a@3 gone)


class TestTimeModel(unittest.TestCase):
    def test_offset_table_exact_roundtrip(self):
        table = OffsetTable([
            (Fraction(0), 0, Fraction(1000)),           # 1 tick = 1 ms
            (Fraction(60), 100_000, Fraction(1000)),    # synthetic jump
        ])
        point = table.wall_to_point(WallClock(Fraction(1500, 1000)))
        self.assertEqual(point, TimePoint(1500))
        jumped = table.wall_to_point(WallClock(61))
        self.assertEqual(jumped, TimePoint(101_000))    # offset carried over
        self.assertEqual(table.point_to_wall(jumped), WallClock(61))
        with self.assertRaises(ValueError):
            table.wall_to_point(WallClock(Fraction(1, 10**6)))  # off-grid

    def test_wallclock_timepoint_duration_are_distinct(self):
        point = TimePoint(100)
        shifted = point + Duration(15)
        self.assertEqual(shifted, TimePoint(115))
        self.assertEqual(shifted - point, Duration(15))
        with self.assertRaises(TypeError):
            point + WallClock(1)                        # wall clock != point
        with self.assertRaises(TypeError):
            point + 15                                  # bare int != duration
        with self.assertRaises(TypeError):
            point - WallClock(1)

    def test_engine_accepts_timepoints(self):
        engine = make_engine()
        engine.add("a", TimePoint(-1))
        total, _ = engine.range_count(TimePoint(-10), TimePoint(0))
        self.assertEqual(total, 1)


class TestRandomVsSortedReference(unittest.TestCase):
    def test_random_updates_match_sorted_reference(self):
        rng = random.Random(20260925)
        engine = make_engine()
        reference = []          # sorted (time, seq, eid)
        live = {}               # eid -> (time, seq)
        seq_counter = 0

        def ref_kth(k):
            t, _s, eid = reference[k - 1]
            return t, eid

        for step in range(600):
            op = rng.random()
            if op < 0.55 or not live:
                eid = f"e{step}"
                t = rng.randint(-120, 120)
                engine.add(eid, t)
                bisect.insort(reference, (t, seq_counter, eid))
                live[eid] = (t, seq_counter)
                seq_counter += 1
            elif op < 0.8:
                eid = rng.choice(list(live))
                old_t, seq = live[eid]
                new_t = rng.randint(-120, 120)
                engine.correct(eid, new_t)
                reference.remove((old_t, seq, eid))
                bisect.insort(reference, (new_t, seq, eid))
                live[eid] = (new_t, seq)
            else:
                eid = rng.choice(list(live))
                old_t, seq = live.pop(eid)
                engine.retract(eid)
                reference.remove((old_t, seq, eid))

            if step % 25 == 0:
                self.assertEqual(engine.size(), len(reference))
                self.assertEqual(engine.items(),
                                 tuple((t, s, e) for t, s, e in reference))
                lo = rng.randint(-150, 100)
                hi = lo + rng.randint(1, 60)
                total, contribs = engine.range_count(lo, hi)
                expect = sum(1 for t, _s, _e in reference if lo <= t < hi)
                self.assertEqual(total, expect)
                self.assertEqual(sum(c.count for c in contribs), expect)
                if reference:
                    k = rng.randint(1, len(reference))
                    self.assertEqual(engine.kth(k), ref_kth(k))
                    q = rng.random()
                    kq = min(len(reference),
                             max(1, math.ceil(q * len(reference))))
                    self.assertEqual(engine.quantile(q), reference[kq - 1][0])


if __name__ == "__main__":
    unittest.main(verbosity=2)
