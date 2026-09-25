import random
import unittest
from datetime import datetime

from bitemporal import (BitemporalStore, Duration, Instant, Timeline,
                        TimeModelError, WallClock)
from bitemporal.checks import check_store, replay_visible
from bitemporal.index import QueryStats
from bitemporal.model import from_key, to_key


def T(ticks):
    return Instant(ticks)


class TimelineTests(unittest.TestCase):
    def setUp(self):
        self.tl = Timeline()
        self.tl.register_offset("CST", Duration.of(hours=8))

    def test_offset_conversion_table_is_exact(self):
        table = self.tl.conversion_table()
        self.assertEqual(table["UTC"], Duration(0))
        self.assertEqual(table["CST"], Duration.of(minutes=480))
        self.assertEqual(table["CST"].ticks, 8 * 3600 * 1_000_000)

    def test_wallclock_to_instant_uses_the_table(self):
        wall = self.tl.wall("CST", datetime(2024, 1, 1, 8, 0))
        utc = self.tl.at("UTC", datetime(2024, 1, 1, 0, 0))
        self.assertEqual(self.tl.instant(wall), utc)
        self.assertEqual(self.tl.wallclock(utc, "CST"), wall)

    def test_unknown_offset_rejected(self):
        with self.assertRaises(KeyError):
            self.tl.wall("MARS", datetime(2024, 1, 1))

    def test_three_types_are_distinct(self):
        instant = Instant(100)
        duration = Duration(5)
        wall = WallClock("UTC", 100)
        self.assertEqual(instant + duration, Instant(105))
        self.assertEqual(instant - duration, Instant(95))
        self.assertEqual(instant - Instant(40), Duration(60))
        self.assertEqual(duration + duration, Duration(10))
        self.assertEqual(duration * 3, Duration(15))
        with self.assertRaises(TimeModelError):
            instant + instant            # two points cannot be added
        with self.assertRaises(TimeModelError):
            wall + duration              # wall clock needs conversion first
        with self.assertRaises(TimeModelError):
            duration + instant           # order does not matter
        with self.assertRaises(TimeModelError):
            instant - wall

    def test_exact_representation_no_floats(self):
        d = Duration.of(days=10_000_000, microseconds=1)
        self.assertIsInstance(d.ticks, int)
        self.assertEqual((Instant(0) + d) - Instant(0), d)
        self.assertEqual(Duration.of(weeks=2), Duration.of(days=14))


class StoreSemanticsTests(unittest.TestCase):
    def setUp(self):
        self.store = BitemporalStore()
        self.JAN, self.FEB, self.MAR, self.APR, self.MAY, self.JUN = (
            T(10), T(20), T(30), T(40), T(50), T(60))

    def upsert(self, *args, **kwargs):
        with self.store.transaction() as tx:
            tx.upsert(*args, **kwargs)
        return tx.entry_ids[0]

    def test_basic_upsert_and_versioned_query(self):
        self.upsert("k", self.JAN, None, value="a")
        self.assertEqual(self.store.value_at("k", self.MAR), "a")
        self.assertEqual(self.store.value_at("k", self.MAR, as_of=1), "a")
        self.assertIsNone(self.store.value_at("k", self.MAR, as_of=0))
        self.assertIsNone(self.store.value_at("k", T(5)))  # before valid_from

    def test_retroactive_correction_keeps_earlier_observation_versions(self):
        self.upsert("k", self.JAN, None, value="old")
        self.upsert("k", self.FEB, self.APR, value="new", priority=10)  # retroactive
        self.assertEqual(self.store.value_at("k", self.MAR, as_of=1), "old")
        self.assertEqual(self.store.value_at("k", self.MAR, as_of=2), "new")
        self.assertEqual(self.store.value_at("k", self.MAR), "new")

    def test_open_ended_interval_is_split_by_new_correction(self):
        self.upsert("k", self.JAN, None, value=1)
        self.upsert("k", self.MAR, self.MAY, value=2, priority=5)
        self.assertEqual(self.store.value_at("k", self.FEB), 1)
        self.assertEqual(self.store.value_at("k", self.APR), 2)
        self.assertEqual(self.store.value_at("k", self.JUN), 1)  # tail survives
        frontier = [f for f in self.store.fragments("k") if f.ver_to is None]
        self.assertEqual(len(frontier), 3)  # open rectangle split into 3

    def test_half_open_boundaries_never_double_hit(self):
        self.upsert("k", self.JAN, self.MAR, value="first")
        self.upsert("k", self.MAR, self.MAY, value="second")
        self.assertEqual(self.store.value_at("k", self.MAR), "second")
        hits = self.store.range_query("k", self.MAR, T(self.MAR.ticks + 1))
        self.assertEqual(len(hits), 1)  # exactly one rectangle owns the boundary
        self.assertEqual(hits[0].value, "second")

    def test_overlap_priority_is_explicit(self):
        self.upsert("k", self.JAN, self.MAY, value="low", priority=1)
        self.upsert("k", self.FEB, self.APR, value="high", priority=9)
        self.assertEqual(self.store.value_at("k", self.MAR), "high")
        self.assertEqual(self.store.value_at("k", T(15)), "low")

    def test_priority_tie_breaks_by_earlier_record(self):
        self.upsert("k", self.JAN, self.MAY, value="first", priority=5)
        self.upsert("k", self.FEB, self.APR, value="second", priority=5)
        self.assertEqual(self.store.value_at("k", self.MAR), "first")

    def test_retraction_does_not_overwrite_history(self):
        base = self.upsert("k", self.JAN, None, value="base")
        corr = self.upsert("k", self.FEB, self.APR, value="fix", priority=10)
        with self.store.transaction() as tx:
            tx.retract(corr)
        self.assertEqual(self.store.version, 3)
        self.assertEqual(self.store.value_at("k", self.MAR), "base")        # latest
        self.assertEqual(self.store.value_at("k", self.MAR, as_of=2), "fix")  # history kept
        self.assertEqual(self.store.retracted_at(corr), 3)
        self.assertIsNone(self.store.retracted_at(base))

    def test_retraction_resurfaces_suppressed_record(self):
        self.upsert("k", self.JAN, None, value="low", priority=1)
        top = self.upsert("k", self.FEB, self.APR, value="top", priority=9)
        self.assertEqual(self.store.value_at("k", self.MAR), "top")
        with self.store.transaction() as tx:
            tx.retract(top)
        self.assertEqual(self.store.value_at("k", self.MAR), "low")

    def test_one_correction_splits_multiple_rectangles(self):
        self.upsert("k", self.JAN, None, value=1)                       # v1
        self.upsert("k", self.FEB, self.MAR, value=2, priority=10)      # v2
        self.upsert("k", self.APR, self.MAY, value=3, priority=10)      # v3
        before = len(self.store.fragments("k"))
        self.upsert("k", T(25), T(45), value=9, priority=20)            # v4
        # v4 overlaps three frontier rectangles ([20,30), [30,40), [40,50))
        # and splits every one of them in a single atomic transaction
        frontier = [f for f in self.store.fragments("k") if f.ver_to is None]
        self.assertEqual(len(self.store.fragments("k")) - before, 3)
        self.assertEqual(len(frontier), 5)
        self.assertEqual(self.store.value_at("k", T(35), as_of=3), 1)   # old view intact
        self.assertEqual(self.store.value_at("k", T(35)), 9)
        self.assertEqual(self.store.value_at("k", T(22)), 2)            # left stub survives
        self.assertEqual(self.store.value_at("k", T(47)), 3)            # right stub survives

    def test_upsert_retract_reprioritize_are_atomic(self):
        a = self.upsert("a", self.JAN, None, value="a")
        b = self.upsert("b", self.JAN, None, value="b", priority=1)
        c = self.upsert("b", self.FEB, self.APR, value="c", priority=2)
        with self.store.transaction() as tx:
            tx.retract(c)
            tx.change_priority(a, 7)
            tx.upsert("c", self.JAN, self.MAR, value="new")
        v = self.store.version
        self.assertEqual(v, 4)                       # one version for all three ops
        self.assertEqual(self.store.value_at("b", self.MAR), "b")
        self.assertEqual(self.store.value_at("b", self.MAR, as_of=v - 1), "c")
        self.assertEqual(self.store.priority_at(a, v), 7)
        self.assertEqual(self.store.value_at("c", self.FEB), "new")

    def test_failed_transaction_changes_nothing(self):
        self.upsert("k", self.JAN, None, value="v")
        version_before = self.store.version
        fragments_before = [f.identity() for f in self.store.fragments("k")]
        with self.assertRaises(KeyError):
            with self.store.transaction() as tx:
                tx.upsert("k", self.FEB, self.MAR, value="x")
                tx.retract(999)  # unknown record -> whole transaction fails
        self.assertEqual(self.store.version, version_before)
        self.assertEqual([f.identity() for f in self.store.fragments("k")],
                         fragments_before)
        self.assertEqual(self.store.value_at("k", T(25)), "v")  # not the rolled-back "x"
        with self.assertRaises(KeyError):
            with self.store.transaction() as tx:
                tx.change_priority(999, 1)

    def test_invalid_interval_rejected(self):
        with self.assertRaises(ValueError):
            self.upsert("k", self.MAR, self.JAN, value="backwards")
        with self.assertRaises(ValueError):
            self.upsert("k", self.MAR, self.MAR, value="empty")

    def test_unknown_observation_version_rejected(self):
        self.upsert("k", self.JAN, None, value="v")
        with self.assertRaises(ValueError):
            self.store.value_at("k", self.JAN, as_of=99)
        with self.assertRaises(ValueError):
            self.store.value_at("k", self.JAN, as_of=-1)


class IndexAndQueryTests(unittest.TestCase):
    def setUp(self):
        self.store = BitemporalStore()
        with self.store.transaction() as tx:                       # v1
            tx.upsert("k", T(0), T(100), value="a", priority=0)
        with self.store.transaction() as tx:                       # v2
            tx.upsert("k", T(20), T(40), value="b", priority=5)
        with self.store.transaction() as tx:                       # v3
            tx.retract(2)

    def test_rectangle_range_query_with_provenance(self):
        frags = self.store.range_query("k", T(10), T(50), ver_from=1, ver_to=3)
        boxes = {(f.valid_from, f.valid_to, f.ver_from, f.ver_to,
                  f.value, f.record_id) for f in frags}
        self.assertEqual(boxes, {
            (T(0), T(100), 1, 2, "a", 1),
            (T(0), T(20), 2, 3, "a", 1),
            (T(20), T(40), 2, 3, "b", 2),
            (T(40), T(100), 2, 3, "a", 1),
        })
        for f in frags:  # provenance: every rectangle names its source record
            rec = self.store.record(f.record_id)
            self.assertEqual(rec.key, "k")
            self.assertLessEqual(rec.version, f.ver_from)

    def test_version_interval_restricts_results(self):
        frags = self.store.range_query("k", None, None, ver_from=3)
        self.assertEqual(len(frags), 1)
        self.assertEqual(frags[0].ver_from, 3)
        self.assertIsNone(frags[0].ver_to)

    def test_explain_reports_winner_and_suppressed(self):
        expl = self.store.explain("k", T(30), as_of=2)
        self.assertEqual(expl.value, "b")
        self.assertEqual(len(expl.competitors), 2)
        winner, loser = expl.competitors
        self.assertEqual(winner.status, "visible")
        self.assertEqual(winner.record_id, 2)
        self.assertIn("lower priority", loser.status)
        self.assertEqual(loser.record_id, 1)

    def test_point_query_does_not_scan_all_history(self):
        store = BitemporalStore()
        records = 0
        for key_i in range(30):
            for v in range(30):
                with store.transaction() as tx:
                    tx.upsert(f"key{key_i}", T(v * 10), T(v * 10 + 10),
                              value=v, priority=v)
                records += 1
        stats = QueryStats()
        value = store.value_at("key17", T(155), stats=stats)
        self.assertEqual(value, 15)
        self.assertLess(stats.examined, store.total_records // 10)
        self.assertLessEqual(stats.examined, 40)

    def test_query_stats_are_optional(self):
        self.assertEqual(self.store.value_at("k", T(30)), "a")


class InvariantTests(unittest.TestCase):
    def test_scripted_scenario_passes_all_checks(self):
        store = BitemporalStore()
        with store.transaction() as tx:
            tx.upsert("x", T(0), None, value=1)
        with store.transaction() as tx:
            tx.upsert("x", T(10), T(30), value=2, priority=5)
            tx.upsert("y", T(5), T(50), value="y")
        with store.transaction() as tx:
            tx.retract(2)
            tx.change_priority(1, 3)
        report = check_store(store)
        self.assertTrue(report.ok, str(report))
        self.assertEqual(report.versions_checked, 3 * 2)

    def test_seeded_fuzz_matches_independent_replay(self):
        rng = random.Random(20240926)
        store = BitemporalStore()
        keys = ["k0", "k1", "k2"]
        live_ids = []
        for _ in range(80):
            with store.transaction() as tx:
                for _ in range(rng.randint(1, 3)):
                    roll = rng.random()
                    if roll < 0.6 or not live_ids:
                        lo = rng.randrange(0, 20) * 10
                        hi = lo + rng.randrange(1, 6) * 10
                        hi = hi if rng.random() < 0.8 else None
                        tx.upsert(rng.choice(keys), T(lo),
                                  None if hi is None else T(hi),
                                  value=rng.randrange(100),
                                  priority=rng.randrange(4))
                    elif roll < 0.8:
                        tx.retract(rng.choice(live_ids))
                    else:
                        tx.change_priority(rng.choice(live_ids), rng.randrange(4))
            for rid in tx.entry_ids:
                rec = store.record(rid)
                if rec.op == "upsert":
                    live_ids.append(rid)
            live_ids = [r for r in live_ids if store.retracted_at(r) is None]
        report = check_store(store)
        self.assertTrue(report.ok, str(report))
        # random point queries agree with brute-force log replay
        for _ in range(300):
            key = rng.choice(keys)
            t = T(rng.randrange(0, 25) * 10 + 5)
            v = rng.randrange(0, store.version + 1)
            expected = None
            for vf, vt, rec in replay_visible(store, key, v):
                if from_key(vf) <= from_key(t) < to_key(vt):
                    expected = rec.value
            self.assertEqual(store.value_at(key, t, as_of=v), expected,
                             f"key={key} t={t} v={v}")


if __name__ == "__main__":
    unittest.main()
