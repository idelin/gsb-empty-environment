import random
import unittest

from bitemporal import (
    BitemporalStore,
    ChangePriority,
    Retract,
    TimePoint,
    Upsert,
    verify_store,
)


def tp(seconds):
    return TimePoint.at(seconds=seconds)


def val(store, version, t):
    frag = store.value_at(version, tp(t))
    return None if frag is None else (frag.record_id, frag.value)


class BasicQueryTest(unittest.TestCase):
    def test_value_at_observation_version(self):
        store = BitemporalStore()
        store.upsert("A", tp(0), tp(10), "v1")
        store.upsert("A", tp(0), tp(10), "v2")  # correction at version 2
        self.assertEqual(val(store, 1, 5), ("A", "v1"))
        self.assertEqual(val(store, 2, 5), ("A", "v2"))
        self.assertIsNone(store.value_at(0, tp(5)))

    def test_future_version_rejected(self):
        store = BitemporalStore()
        store.upsert("A", tp(0), tp(10), 1)
        with self.assertRaises(ValueError):
            store.value_at(2, tp(5))

    def test_empty_result_outside_interval(self):
        store = BitemporalStore()
        store.upsert("A", tp(0), tp(10), 1)
        self.assertIsNone(store.value_at(1, tp(10)))
        self.assertIsNone(store.value_at(1, tp(-1)))


class BoundaryTest(unittest.TestCase):
    def test_boundary_not_double_hit(self):
        store = BitemporalStore()
        store.upsert("A", tp(0), tp(5), "left")
        store.upsert("B", tp(5), tp(9), "right", priority=0)
        # exactly on the boundary: belongs to the interval starting there
        self.assertEqual(val(store, 2, 5), ("B", "right"))
        # just before: microsecond precision, exact
        before = tp(5) - (tp(1) - tp(0)) / 1_000_000
        self.assertEqual(val(store, 2, before.micros / 1_000_000), ("A", "left"))

    def test_adjacent_same_record_merges_canonically(self):
        store = BitemporalStore()
        store.upsert("A", tp(0), tp(5), "x")
        store.upsert("B", tp(5), tp(9), "y")
        store.retract("B")
        # after retraction the gap closes; fragments stay disjoint
        frags = store.scan(3, None, tp(0), tp(9))
        self.assertEqual([(f.record_id, f.valid_from, f.valid_to)
                          for f in frags], [("A", tp(0), tp(5))])


class RetroactiveCorrectionTest(unittest.TestCase):
    def setUp(self):
        self.store = BitemporalStore()
        self.store.upsert("A", tp(0), tp(10), 100, priority=0)   # v1
        self.store.upsert("B", tp(4), tp(6), 200, priority=5)    # v2
        self.store.upsert("C", tp(8), None, 300, priority=1)     # v3 open ended

    def test_correction_splits_many_rectangles(self):
        before = self.store.index_stats()["rectangles"]
        # one retroactive correction overlapping A, B and C regions
        self.store.upsert("D", tp(1), tp(9), 999, priority=9)    # v4
        after = self.store.index_stats()["rectangles"]
        self.assertGreater(after - before, 3)
        self.assertEqual(val(self.store, 4, 2), ("D", 999))
        self.assertEqual(val(self.store, 4, 5), ("D", 999))
        self.assertEqual(val(self.store, 4, 8), ("D", 999))
        self.assertEqual(val(self.store, 4, 0), ("A", 100))
        self.assertEqual(val(self.store, 4, 9), ("C", 300))

    def test_earlier_versions_unchanged_by_later_correction(self):
        snapshot = {t: val(self.store, 2, t) for t in range(0, 12)}
        self.store.upsert("D", tp(1), tp(9), 999, priority=9)   # v4
        self.store.retract("A")                                   # v5
        for t, expected in snapshot.items():
            self.assertEqual(val(self.store, 2, t), expected, f"t={t}")

    def test_open_ended_interval_split_by_new_correction(self):
        # C is open ended [8, inf); a later correction cuts into it
        self.store.upsert("E", tp(12), tp(15), 500, priority=7)  # v4
        self.assertEqual(val(self.store, 4, 11), ("C", 300))
        self.assertEqual(val(self.store, 4, 13), ("E", 500))
        self.assertEqual(val(self.store, 4, 16), ("C", 300))
        # and the earlier observation of C is untouched
        self.assertEqual(val(self.store, 3, 13), ("C", 300))


class RetractionTest(unittest.TestCase):
    def test_retraction_does_not_overwrite_history(self):
        store = BitemporalStore()
        store.upsert("A", tp(0), tp(10), 100)   # v1
        store.upsert("A", tp(0), tp(10), 101)   # v2 correction
        store.retract("A")                       # v3
        self.assertIsNone(store.value_at(3, tp(5)))
        self.assertEqual(val(store, 1, 5), ("A", 100))
        self.assertEqual(val(store, 2, 5), ("A", 101))
        history = store.history_of("A")
        self.assertEqual(len(history), 2)
        self.assertEqual(history[0].version_to, 2)
        self.assertEqual(history[1].version_to, 3)
        self.assertEqual(history[0].value, 100)

    def test_retract_unknown_record_fails(self):
        store = BitemporalStore()
        with self.assertRaises(KeyError):
            store.retract("ghost")


class PriorityTest(unittest.TestCase):
    def test_explicit_priority_resolves_overlap(self):
        store = BitemporalStore()
        store.upsert("low", tp(0), tp(10), "L", priority=1)
        store.upsert("high", tp(3), tp(7), "H", priority=2)
        self.assertEqual(val(store, 2, 1), ("low", "L"))
        self.assertEqual(val(store, 2, 5), ("high", "H"))
        self.assertEqual(val(store, 2, 8), ("low", "L"))

    def test_tie_break_is_deterministic_on_record_id(self):
        store = BitemporalStore()
        store.upsert("aaa", tp(0), tp(10), "first")
        store.upsert("bbb", tp(0), tp(10), "second")
        self.assertEqual(val(store, 2, 5), ("bbb", "second"))

    def test_priority_change_is_atomic_and_versioned(self):
        store = BitemporalStore()
        store.upsert("a", tp(0), tp(10), "A", priority=1)   # v1
        store.upsert("b", tp(0), tp(10), "B", priority=2)   # v2
        store.change_priority("a", 5)                        # v3
        self.assertEqual(val(store, 2, 5), ("b", "B"))
        self.assertEqual(val(store, 3, 5), ("a", "A"))


class AtomicTransactionTest(unittest.TestCase):
    def test_batch_applies_as_one_version(self):
        store = BitemporalStore()
        store.upsert("a", tp(0), tp(5), 1)  # v1
        v = store.transact([
            Upsert("b", tp(0), tp(5), 2, priority=1),
            Retract("a"),
            Upsert("c", tp(10), tp(20), 3),
        ])
        self.assertEqual(v, 2)
        self.assertEqual(val(store, 2, 2), ("b", 2))
        self.assertEqual(val(store, 2, 15), ("c", 3))
        self.assertEqual(val(store, 1, 2), ("a", 1))

    def test_failed_batch_changes_nothing(self):
        store = BitemporalStore()
        store.upsert("a", tp(0), tp(5), 1)  # v1
        with self.assertRaises(KeyError):
            store.transact([Upsert("b", tp(0), tp(5), 2), Retract("ghost")])
        self.assertEqual(store.version, 1)
        self.assertEqual(val(store, 1, 2), ("a", 1))
        self.assertEqual(store.history_of("b"), [])

    def test_invalid_interval_rejected(self):
        store = BitemporalStore()
        with self.assertRaises(ValueError):
            store.upsert("a", tp(5), tp(5), 1)
        with self.assertRaises(ValueError):
            store.upsert("a", tp(6), tp(5), 1)
        self.assertEqual(store.version, 0)


class ScanTest(unittest.TestCase):
    def setUp(self):
        self.store = BitemporalStore()
        self.store.upsert("A", tp(0), tp(10), 100)        # v1
        self.store.upsert("B", tp(4), None, 200, priority=1)  # v2
        self.store.upsert("A", tp(2), tp(8), 150)         # v3

    def test_range_scan_returns_clipped_rectangles(self):
        frags = self.store.scan(0, None, tp(3), tp(5))
        spans = {(f.record_id, f.valid_from, f.valid_to,
                  f.version_from, f.version_to) for f in frags}
        self.assertIn(("A", tp(3), tp(4), 1, 3), spans)
        self.assertIn(("A", tp(3), tp(4), 3, None), spans)
        self.assertIn(("A", tp(4), tp(5), 1, 2), spans)
        self.assertIn(("B", tp(4), tp(5), 2, None), spans)

    def test_scan_fragments_are_disjoint_per_version(self):
        frags = self.store.scan(0, None, None, None)
        for version in range(0, 4):
            mine = [f for f in frags
                    if f.version_from <= version
                    and (f.version_to is None or version < f.version_to)]
            mine.sort(key=lambda f: (f.valid_from is None, f.valid_from))
            for a, b in zip(mine, mine[1:]):
                self.assertIsNotNone(a.valid_to)
                self.assertLessEqual(a.valid_to, b.valid_from)

    def test_version_window_scan(self):
        frags = self.store.scan(2, 3, None, None)
        for f in frags:
            self.assertLess(f.version_from, 3)
            self.assertTrue(f.version_to is None or f.version_to > 2)


class ProvenanceTest(unittest.TestCase):
    def test_explain_reports_winner_and_shadowed(self):
        store = BitemporalStore()
        store.upsert("a", tp(0), tp(10), "A", priority=1)
        store.upsert("b", tp(2), tp(8), "B", priority=3)
        exp = store.explain(2, tp(5))
        self.assertEqual(exp.fragment.record_id, "b")
        by_id = {c.record_id: c for c in exp.contenders}
        self.assertTrue(by_id["b"].visible)
        self.assertFalse(by_id["a"].visible)
        self.assertIn("priority", by_id["a"].reason)

    def test_explain_tie_break_reason(self):
        store = BitemporalStore()
        store.upsert("a", tp(0), tp(10), "A", priority=1)
        store.upsert("b", tp(0), tp(10), "B", priority=1)
        exp = store.explain(2, tp(5))
        loser = [c for c in exp.contenders if not c.visible][0]
        self.assertIn("tie", loser.reason)

    def test_explain_empty_region(self):
        store = BitemporalStore()
        store.upsert("a", tp(0), tp(10), "A")
        exp = store.explain(1, tp(50))
        self.assertIsNone(exp.fragment)
        self.assertEqual(exp.contenders, ())

    def test_provenance_carries_source_version(self):
        store = BitemporalStore()
        store.upsert("a", tp(0), tp(10), "A")   # v1
        store.upsert("a", tp(0), tp(10), "A2")  # v2
        frag = store.value_at(2, tp(5))
        self.assertEqual(frag.version_from, 2)
        old = store.value_at(1, tp(5))
        self.assertEqual(old.version_from, 1)


class QueryEfficiencyTest(unittest.TestCase):
    def test_point_query_does_not_scan_history(self):
        store = BitemporalStore()
        # deep history in one region: 300 retroactive corrections
        for i in range(300):
            store.upsert("hot", tp(0), tp(10), i)
        # separate quiet region far away
        store.upsert("cold", tp(1000), tp(1010), "cold")
        total_rectangles = store.index_stats()["rectangles"]
        self.assertGreater(total_rectangles, 300)

        store.reset_probes()
        store.value_at(store.version, tp(1005))
        probes_quiet = store.probes
        self.assertLessEqual(probes_quiet, 8)

        store.reset_probes()
        store.value_at(7, tp(5))
        probes_deep = store.probes
        self.assertLess(probes_deep, total_rectangles // 10)

    def test_range_scan_touches_only_intersecting_rectangles(self):
        store = BitemporalStore()
        for i in range(200):
            store.upsert(f"r{i}", tp(1000 * i), tp(1000 * i + 10), i)
        store.reset_probes()
        frags = store.scan(0, None, tp(5000), tp(5010))
        self.assertTrue(frags)
        self.assertLess(store.probes, 40)


class RandomizedInvariantTest(unittest.TestCase):
    def test_random_workload_matches_naive_model(self):
        rng = random.Random(20260926)
        store = BitemporalStore()
        live: set[str] = set()
        for _ in range(60):
            ops = []
            for _ in range(rng.randint(1, 3)):
                kind = rng.random()
                if kind < 0.55 or not live:
                    rid = f"r{rng.randint(0, 7)}"
                    start = rng.randint(0, 30)
                    if rng.random() < 0.25:
                        end = None
                    else:
                        end = start + rng.randint(1, 20)
                    ops.append(Upsert(rid, tp(start),
                                      None if end is None else tp(end),
                                      rng.randint(0, 999),
                                      priority=rng.randint(0, 3)))
                    live.add(rid)
                elif kind < 0.8:
                    rid = rng.choice(sorted(live))
                    ops.append(Retract(rid))
                    live.discard(rid)
                else:
                    rid = rng.choice(sorted(live))
                    ops.append(ChangePriority(rid, rng.randint(0, 3)))
            try:
                store.transact(ops)
            except KeyError:
                pass  # double retract inside one batch: rejected atomically
        report = verify_store(store)
        report.raise_if_failed()
        self.assertGreater(report.points_checked, 100)


class VerifyTest(unittest.TestCase):
    def test_verify_passes_on_consistent_store(self):
        store = BitemporalStore()
        store.upsert("a", tp(0), tp(10), 1)
        store.upsert("b", tp(5), None, 2, priority=1)
        store.retract("a")
        report = verify_store(store)
        self.assertTrue(report.ok)
        self.assertEqual(report.versions_checked, 4)

    def test_verify_detects_tampered_index(self):
        store = BitemporalStore()
        store.upsert("a", tp(0), tp(10), 1)
        # corrupt the index from the outside: verifier must catch it
        seg = store._visible._segments[1]
        last = seg.entries[-1]
        from bitemporal.rectindex import Entry
        from bitemporal.store import _VisiblePayload
        seg.entries[-1] = Entry(last.version_from, last.version_to,
                                _VisiblePayload("a", 999, 0))
        report = verify_store(store)
        self.assertFalse(report.ok)


if __name__ == "__main__":
    unittest.main()
