import unittest
from fractions import Fraction

from logmerge import (
    CAUSAL,
    LOCAL,
    TIME,
    ConflictError,
    Contradiction,
    Duration,
    Event,
    LogStore,
    MergeResult,
    Merger,
    OffsetTable,
    TimePoint,
    WallClock,
    enumerate_extensions,
    verify_necessity,
)

OFFSETS = OffsetTable({"A": 0, "B": 2, "C": "-1/2"})


def ev(eid, source, seq, reading, error=0, payload=None, causal_after=()):
    return Event(
        eid, source, seq, WallClock(source, reading), Duration(error),
        payload if payload is not None else f"payload-{eid}",
        causal_after=causal_after,
    )


class TestTimeValues(unittest.TestCase):
    def test_exact_fraction_arithmetic(self):
        t = TimePoint("0.1") + Duration("0.2")
        self.assertEqual(t.value, Fraction(3, 10))
        self.assertEqual((t - TimePoint("0.1")).value, Fraction(1, 5))

    def test_types_are_distinct(self):
        self.assertIsInstance(TimePoint(1) - TimePoint(2), Duration)
        self.assertIsInstance(TimePoint(1) + Duration(2), TimePoint)
        with self.assertRaises(TypeError):
            TimePoint(1) + TimePoint(2)
        with self.assertRaises(TypeError):
            WallClock("A", 1) + Duration(1)

    def test_offset_conversion_exact(self):
        table = OffsetTable({"S": "1/3"})
        ref = table.to_reference(WallClock("S", "1/6"))
        self.assertEqual(ref.value, Fraction(1, 2))

    def test_unknown_source_rejected(self):
        with self.assertRaises(KeyError):
            OFFSETS.to_reference(WallClock("ZZZ", 1))


class TestMerge(unittest.TestCase):
    def test_local_order_preserved_and_stable(self):
        events = [
            ev("b2", "B", 1, 103, 1),
            ev("a2", "A", 1, 103, 1),
            ev("b1", "B", 0, 99, 1),
            ev("a1", "A", 0, 100, 1),
        ]
        r1 = Merger(OFFSETS).merge(events)
        r2 = Merger(OFFSETS).merge(list(reversed(events)))
        self.assertIsInstance(r1, MergeResult)
        self.assertEqual(r1.ids, r2.ids)
        self.assertLess(r1.ids.index("a1"), r1.ids.index("a2"))
        self.assertLess(r1.ids.index("b1"), r1.ids.index("b2"))

    def test_causality_beats_apparent_wallclock_reversal(self):
        events = [
            ev("a1", "A", 0, 100, 1),
            ev("b1", "B", 0, "98.5", "1/2", causal_after=("a1",)),
        ]
        result = Merger(OFFSETS).merge(events)
        self.assertIsInstance(result, MergeResult)
        self.assertEqual(result.ids, ("a1", "b1"))
        self.assertTrue(result.order[0].next_necessary)
        self.assertIn(("a1", "b1"), result.necessary)

    def test_uncertain_when_intervals_overlap_without_causality(self):
        events = [ev("a1", "A", 0, 100, 2), ev("c1", "C", 0, 101, 2)]
        result = Merger(OFFSETS).merge(events)
        self.assertIsInstance(result, MergeResult)
        self.assertFalse(result.order[0].next_necessary)
        self.assertEqual(result.necessary, frozenset())
        exts, complete = enumerate_extensions(list(result.ids), list(result.edges))
        self.assertTrue(complete)
        self.assertEqual(len(exts), 2)

    def test_time_implied_necessary_order(self):
        events = [ev("late", "C", 0, 110, 1), ev("early", "A", 0, 100, 1)]
        result = Merger(OFFSETS).merge(events)
        self.assertEqual(result.ids, ("early", "late"))
        self.assertTrue(result.order[0].next_necessary)
        kinds = {(e.src, e.dst): e.kind for e in result.edges}
        self.assertEqual(kinds[("early", "late")], TIME)

    def test_last_event_mark_is_none(self):
        result = Merger(OFFSETS).merge([ev("a1", "A", 0, 1, 0)])
        self.assertIsNone(result.order[-1].next_necessary)


class TestContradictions(unittest.TestCase):
    def test_causal_cycle_returned(self):
        events = [
            ev("x", "A", 0, 10, 5, causal_after=("y",)),
            ev("y", "B", 0, 10, 5, causal_after=("x",)),
        ]
        result = Merger(OFFSETS).merge(events)
        self.assertIsInstance(result, Contradiction)
        self.assertEqual(result.reason, "cycle")
        self.assertEqual({e.kind for e in result.cycle}, {CAUSAL})
        self.assertEqual({e.src for e in result.cycle}, {"x", "y"})

    def test_causal_edge_contradicting_time_gives_cycle(self):
        events = [
            ev("x", "A", 0, 10, 0, causal_after=("y",)),
            ev("y", "A", 1, 20, 0),
        ]
        result = Merger(OFFSETS).merge(events)
        self.assertIsInstance(result, Contradiction)
        self.assertEqual(result.reason, "cycle")
        kinds = {e.kind for e in result.cycle}
        self.assertIn(CAUSAL, kinds)
        self.assertTrue(kinds & {TIME, LOCAL})

    def test_infeasible_time_chain_returned_not_modified(self):
        events = [
            ev("p", "A", 0, 10, 0),
            ev("q", "A", 1, 10, 0, causal_after=("p",)),
        ]
        result = Merger(OFFSETS).merge(events)
        self.assertIsInstance(result, Contradiction)
        self.assertEqual(result.reason, "infeasible-time")
        self.assertEqual(result.chain, ("p", "q"))

    def test_infeasible_chain_through_three_events(self):
        events = [
            ev("a", "A", 0, 10, 0),
            ev("b", "B", 0, "8.25", "1/4", causal_after=("a",)),
            ev("c", "C", 0, "10.5", 0, causal_after=("b",)),
        ]
        result = Merger(OFFSETS).merge(events)
        self.assertIsInstance(result, Contradiction)
        self.assertEqual(result.reason, "infeasible-time")
        self.assertEqual(result.chain, ("a", "b", "c"))

    def test_unknown_causal_dependency_rejected(self):
        with self.assertRaises(KeyError):
            Merger(OFFSETS).merge([ev("a", "A", 0, 1, 0, causal_after=("ghost",))])


class TestEnumerationVerification(unittest.TestCase):
    def test_necessity_matches_full_enumeration(self):
        events = [
            ev("a1", "A", 0, 100, 1),
            ev("a2", "A", 1, 103, 1),
            ev("b1", "B", 0, "98.5", "1/2", causal_after=("a1",)),
            ev("b2", "B", 1, 103, 1),
            ev("c1", "C", 0, 101, 3),
        ]
        result = Merger(OFFSETS).merge(events)
        self.assertIsInstance(result, MergeResult)
        problems = verify_necessity(
            list(result.ids), list(result.edges), result.necessary
        )
        self.assertEqual(problems, [])

    def test_uncertain_marks_have_counterexample_extension(self):
        events = [
            ev("a1", "A", 0, 100, 1),
            ev("b1", "B", 0, "98.5", "1/2", causal_after=("a1",)),
            ev("c1", "C", 0, 101, 3),
        ]
        result = Merger(OFFSETS).merge(events)
        exts, complete = enumerate_extensions(list(result.ids), list(result.edges))
        self.assertTrue(complete)
        for i, m in enumerate(result.order):
            if m.next_necessary is False:
                a, b = result.ids[i], result.ids[i + 1]
                self.assertTrue(
                    any(x.index(b) < x.index(a) for x in exts),
                    f"uncertain pair ({a}, {b}) has no swapped extension",
                )


class TestStore(unittest.TestCase):
    def _store(self):
        store = LogStore(OFFSETS)
        store.add_event(ev("a1", "A", 0, 100, 1))
        store.add_event(ev("a2", "A", 1, 103, 1))
        store.add_event(ev("c1", "C", 0, 101, 3))
        return store

    def test_conflicting_same_id_rejected(self):
        store = self._store()
        with self.assertRaises(ConflictError):
            store.add_event(ev("a1", "A", 0, 100, 1, payload="tampered"))
        store.add_event(ev("a1", "A", 0, 100, 1))

    def test_revision_produces_delta_and_keeps_old_version(self):
        store = self._store()
        v1 = store.publish()
        rev = store.revise([ev("c1", "C", 0, 105, "1/2")])
        self.assertEqual(rev.old.number, v1.number)
        self.assertNotEqual(rev.old.result.ids, rev.new.result.ids)
        moved = {eid: (o, n) for eid, o, n in rev.delta.moved}
        self.assertIn("c1", moved)
        self.assertEqual(moved["c1"][0], 0)
        self.assertGreater(moved["c1"][1], moved["c1"][0])
        old = store.get(v1.number)
        self.assertEqual(old.result.ids, v1.result.ids)
        self.assertEqual(store.current.number, rev.new.number)

    def test_revision_of_unknown_event_rejected(self):
        store = self._store()
        store.publish()
        with self.assertRaises(KeyError):
            store.revise([ev("ghost", "C", 0, 1, 0)])

    def test_delta_necessity_changes_tracked(self):
        store = LogStore(OFFSETS)
        store.add_event(ev("a1", "A", 0, 100, 2))
        store.add_event(ev("c1", "C", 0, 101, 2))
        store.publish()
        rev = store.revise([ev("c1", "C", 0, 110, 1)])
        self.assertIn(("a1", "c1"), rev.delta.became_necessary)
        self.assertEqual(rev.delta.became_uncertain, ())


if __name__ == "__main__":
    unittest.main()
