import unittest
from fractions import Fraction

from logmerge import (
    CausalCycleError,
    Event,
    InfeasibleError,
    OffsetInterval,
    OffsetTable,
    WallClock,
    merge,
)

TABLE = OffsetTable({
    "web": OffsetInterval(-5, 5),
    "db": OffsetInterval(-5, 5),
    "cache": OffsetInterval(90, 110),
})

EVENTS = [
    Event("w1", "web", WallClock(1000, "web"), "recv request"),
    Event("w2", "web", WallClock(1050, "web"), "send response", causes=("d2",)),
    Event("d1", "db", WallClock(1005, "db"), "query", causes=("w1",)),
    Event("d2", "db", WallClock(1020, "db"), "write"),
    Event("c1", "cache", WallClock(960, "cache"), "invalidate", causes=("d2",)),
]


class TestMainScenario(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.result = merge(EVENTS, TABLE)

    def test_stable_order(self):
        self.assertEqual(self.result.order, ("w1", "d1", "d2", "w2", "c1"))

    def test_deterministic(self):
        again = merge(list(EVENTS), TABLE)
        self.assertEqual(self.result.order, again.order)

    def test_wallclock_reversal_follows_causality(self):
        # c1's wall clock (960) is the smallest reading, yet causality
        # (w1 -> d1 -> d2 -> c1) and the offset table place it after d2.
        self.assertLess(self.result.position("w1"), self.result.position("c1"))
        self.assertIn(("d2", "c1"), self.result.necessary_pairs)

    def test_uncertain_pair(self):
        self.assertEqual(self.result.uncertain_pairs, frozenset({("c1", "w2")}))
        marks = {(m.before, m.after): m.necessary for m in self.result.adjacent}
        self.assertFalse(marks[("w2", "c1")])
        self.assertTrue(marks[("d2", "w2")])

    def test_time_bounds(self):
        lo, hi = self.result.time_bounds["c1"]
        self.assertEqual((lo.at, hi.at), (1050, 1070))
        lo, hi = self.result.time_bounds["w1"]
        self.assertEqual((lo.at, hi.at), (995, 1005))


class TestOverlapVsSeparation(unittest.TestCase):
    def test_overlapping_intervals_are_uncertain(self):
        table = OffsetTable({
            "a": OffsetInterval(-10, 10),
            "b": OffsetInterval(-10, 10),
        })
        events = [
            Event("e1", "a", WallClock(100, "a")),
            Event("e2", "b", WallClock(105, "b")),
        ]
        result = merge(events, table)
        self.assertEqual(result.uncertain_pairs, frozenset({("e1", "e2")}))
        self.assertEqual(len(result.adjacent), 1)
        self.assertFalse(result.adjacent[0].necessary)

    def test_strictly_separated_is_necessary(self):
        table = OffsetTable({
            "a": OffsetInterval(-10, 10),
            "b": OffsetInterval(-10, 10),
        })
        events = [
            Event("e1", "a", WallClock(100, "a")),
            Event("e2", "b", WallClock(121, "b")),  # earliest 111 > latest of e1 (110)
        ]
        result = merge(events, table)
        self.assertIn(("e1", "e2"), result.necessary_pairs)
        self.assertEqual(result.uncertain_pairs, frozenset())

    def test_exact_boundary_is_uncertain(self):
        # t(e1) <= 1 exactly equals t(e2) == 1: no strict separation.
        table = OffsetTable({
            "a": OffsetInterval(0, 1),
            "b": OffsetInterval(0, 0),
        })
        events = [
            Event("e1", "a", WallClock(0, "a")),
            Event("e2", "b", WallClock(1, "b")),
        ]
        result = merge(events, table)
        self.assertEqual(result.uncertain_pairs, frozenset({("e1", "e2")}))

    def test_exact_fractions_decide_strictness(self):
        table = OffsetTable({
            "a": OffsetInterval(0, 1),
            "b": OffsetInterval(0, 0),
        })
        events = [
            Event("e1", "a", WallClock(0, "a")),
            Event("e2", "b", WallClock(Fraction(1_000_001, 1_000_000), "b")),
        ]
        result = merge(events, table)
        self.assertIn(("e1", "e2"), result.necessary_pairs)


class TestContradictions(unittest.TestCase):
    def test_infeasible_returns_constraint_ring(self):
        events = [
            Event("w1", "web", WallClock(1000, "web"), causes=("c1",)),
            Event("c1", "cache", WallClock(960, "cache")),
        ]
        with self.assertRaises(InfeasibleError) as ctx:
            merge(events, TABLE)
        ring = ctx.exception.ring
        self.assertTrue(ring)
        kinds = {c.kind for c in ring}
        self.assertIn("causal", kinds)
        self.assertIn("offset", kinds)
        self.assertIn("wall", kinds)

    def test_causal_cycle_detected(self):
        events = [
            Event("a", "web", WallClock(1, "web"), causes=("b",)),
            Event("b", "web", WallClock(2, "web"), causes=("a",)),
        ]
        with self.assertRaises(CausalCycleError) as ctx:
            merge(events, TABLE)
        self.assertEqual(set(ctx.exception.cycle), {"a", "b"})

    def test_local_order_contradicts_wallclock(self):
        # Same source, readings go backwards: no offset can fix that.
        events = [
            Event("a", "web", WallClock(100, "web")),
            Event("b", "web", WallClock(90, "web")),
        ]
        with self.assertRaises(InfeasibleError):
            merge(events, TABLE)

    def test_unknown_cause_and_source(self):
        with self.assertRaises(ValueError):
            merge([Event("a", "web", WallClock(1, "web"), causes=("ghost",))], TABLE)
        with self.assertRaises(ValueError):
            merge([Event("a", "nowhere", WallClock(1, "nowhere"))], TABLE)


if __name__ == "__main__":
    unittest.main()
