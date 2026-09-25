"""必然偏序推导的单元测试。"""

import random
import unittest

from temporal_po import (
    CausalEdge,
    Duration,
    IntervalConstraint,
    TimePoint,
    TemporalOrder,
    UnitEntry,
    UnitTable,
    WallClock,
)
from datetime import datetime, timezone


class IntervalSemanticsTests(unittest.TestCase):
    def test_disjoint_closed_intervals_force_order(self):
        order = TemporalOrder(default_domain=(0, 10))
        order.add_interval("iA", "A", 0, 2)
        order.add_interval("iB", "B", 3, 4)
        result = order.analyze()
        self.assertTrue(result.feasible)
        self.assertEqual(
            [(r.earlier, r.later) for r in result.closure], [("A", "B")]
        )
        self.assertEqual(result.incomparable, ())
        self.assertTrue(result.enum_matches_closure)

    def test_touching_closed_intervals_are_incomparable(self):
        # 题目样例：A=[0,2] 与 [1,3] 重叠 → 不可比较
        order = TemporalOrder(default_domain=(0, 10))
        order.add_interval("iA", "A", 0, 2)
        order.add_interval("iC", "C", 1, 3)
        result = order.analyze()
        self.assertEqual(result.closure, ())
        self.assertEqual(result.incomparable, (("A", "C"),))
        # 相接 [0,2] 与 [2,3]：可同时取 2，仍不可比较
        order2 = TemporalOrder(default_domain=(0, 10))
        order2.add_interval("iA", "A", 0, 2)
        order2.add_interval("iD", "D", 2, 3)
        r2 = order2.analyze()
        self.assertEqual(r2.closure, ())
        self.assertEqual(r2.incomparable, (("A", "D"),))

    def test_no_midpoint_total_order(self):
        # 重叠区间不允许凭区间中点排出全序
        order = TemporalOrder(default_domain=(0, 100))
        order.add_interval("iA", "A", 0, 100)
        order.add_interval("iB", "B", 40, 60)
        result = order.analyze()
        self.assertEqual(result.closure, ())
        self.assertEqual(result.incomparable, (("A", "B"),))
        # 两个方向都要有见证
        sols, _ = order.enumerate_solutions()
        self.assertTrue(any(s["A"] < s["B"] for s in sols))
        self.assertTrue(any(s["A"] >= s["B"] for s in sols))


class LayersTests(unittest.TestCase):
    def test_determined_layers_and_within_layer_incomparable(self):
        order = TemporalOrder(default_domain=(0, 20))
        order.add_causal("c1", "a", "c", 1)
        order.add_causal("c2", "b", "c", 1)
        order.add_causal("c3", "c", "d", 1)
        result = order.analyze()
        self.assertEqual(result.layers, (("a", "b"), ("c",), ("d",)))
        self.assertIn(("a", "b"), result.incomparable)
        pairs = {(r.earlier, r.later) for r in result.relations}
        self.assertEqual(pairs, {("a", "c"), ("b", "c"), ("c", "d")})


class InputOrderTests(unittest.TestCase):
    def test_input_order_does_not_affect_edges(self):
        def build(seq):
            order = TemporalOrder(default_domain=(0, 12))
            for cid in seq:
                if cid == "iA":
                    order.add_interval("iA", "A", 0, 2)
                elif cid == "iB":
                    order.add_interval("iB", "B", 3, 5)
                elif cid == "eAB":
                    order.add_causal("eAB", "A", "B", 2)
                elif cid == "eBC":
                    order.add_causal("eBC", "B", "C", 1)
            return order.analyze()

        seq_a = ["iA", "eBC", "iB", "eAB"]
        seq_b = list(reversed(seq_a))
        ra, rb = build(seq_a), build(seq_b)
        key = lambda r: [(x.earlier, x.later, x.gap, x.support) for x in r.relations]
        self.assertEqual(key(ra), key(rb))
        self.assertEqual(ra.layers, rb.layers)
        self.assertEqual(ra.incomparable, rb.incomparable)


class RetractionTests(unittest.TestCase):
    def test_retract_direct_removes_unsupported_relation(self):
        order = TemporalOrder(default_domain=(0, 12))
        order.add_interval("iA", "A", 0, 2)
        order.add_interval("iB", "B", 0, 12)
        order.add_causal("eAB", "A", "B", 1)
        self.assertEqual(
            [(r.earlier, r.later) for r in order.analyze().closure], [("A", "B")]
        )
        order.retract("eAB")
        self.assertEqual(order.analyze().closure, ())
        self.assertEqual(order.analyze().incomparable, (("A", "B"),))

    def test_retract_middle_edge_drops_transitive_chain(self):
        order = TemporalOrder(default_domain=(0, 50))
        order.add_event("A")
        order.add_event("B")
        order.add_event("C")
        order.add_causal("e1", "A", "B", 1)
        order.add_causal("e2", "B", "C", 1)
        before = {(r.earlier, r.later) for r in order.analyze().closure}
        self.assertEqual(before, {("A", "B"), ("B", "C"), ("A", "C")})
        order.retract("e1")
        after = {(r.earlier, r.later) for r in order.analyze().closure}
        self.assertEqual(after, {("B", "C")})

    def test_correct_interval_changes_order(self):
        order = TemporalOrder(default_domain=(0, 10))
        order.add_interval("iP", "P", 0, 2)
        order.add_interval("iQ", "Q", 6, 8)
        self.assertEqual(
            [(r.earlier, r.later) for r in order.analyze().closure], [("P", "Q")]
        )
        order.correct_interval("iQ", low=1, high=2)
        result = order.analyze()
        self.assertEqual(result.closure, ())
        self.assertEqual(result.incomparable, (("P", "Q"),))


class ContradictionTests(unittest.TestCase):
    def test_causal_positive_cycle_with_evidence(self):
        order = TemporalOrder(default_domain=(0, 20))
        order.add_event("R")
        order.add_event("S")
        order.add_event("T")
        order.add_causal("g1", "R", "S", 1)
        order.add_causal("g2", "S", "T", 1)
        order.add_causal("g3", "T", "R", 2)
        result = order.analyze()
        self.assertFalse(result.feasible)
        self.assertEqual(len(result.cycles), 1)
        cyc = result.cycles[0]
        self.assertFalse(cyc.due_to_bounds)
        self.assertEqual(cyc.total_gap, 4)
        self.assertEqual(set(cyc.nodes), {"R", "S", "T"})
        joined = " ".join(cyc.edges)
        for cid in ("g1", "g2", "g3"):
            self.assertIn(cid, joined)
        self.assertEqual(order.enumerate_solutions()[0], [])

    def test_interval_bound_cycle_with_original_evidence(self):
        order = TemporalOrder(default_domain=(0, 10))
        order.add_interval("iP", "P", 5, 9)
        order.add_interval("iQ", "Q", 0, 2)
        order.add_causal("ePQ", "P", "Q", 1)
        result = order.analyze()
        self.assertFalse(result.feasible)
        self.assertTrue(any(c.due_to_bounds for c in result.cycles))
        text = " ".join(e for c in result.cycles for e in c.edges)
        for cid in ("iP", "iQ", "ePQ"):
            self.assertIn(cid, text)

    def test_self_causal_edge_rejected(self):
        with self.assertRaises(ValueError):
            CausalEdge("x", "A", "A", 1)

    def test_empty_interval_rejected(self):
        with self.assertRaises(ValueError):
            IntervalConstraint("x", "A", 5, 4)


class WitnessTests(unittest.TestCase):
    def test_witness_satisfies_all_constraints(self):
        order = TemporalOrder(default_domain=(0, 30))
        order.add_interval("iA", "A", 2, 6)
        order.add_interval("iB", "B", 0, 4)
        order.add_interval("iC", "C", 10, 20)
        order.add_causal("e1", "A", "C", 3)
        order.add_causal("e2", "B", "C", 1)
        result = order.analyze()
        w = result.witness.assignment
        self.assertTrue(2 <= w["A"] <= 6)
        self.assertTrue(0 <= w["B"] <= 4)
        self.assertTrue(10 <= w["C"] <= 20)
        self.assertGreaterEqual(w["C"] - w["A"], 3)
        self.assertGreaterEqual(w["C"] - w["B"], 1)

    def test_guaranteed_gap_is_tight(self):
        order = TemporalOrder(default_domain=(0, 20))
        order.add_interval("iA", "A", 0, 2)
        order.add_interval("iB", "B", 3, 4)
        rel = [r for r in order.analyze().closure if (r.earlier, r.later) == ("A", "B")]
        self.assertEqual(rel[0].gap, 1)
        sols, _ = order.enumerate_solutions()
        self.assertEqual(min(s["B"] - s["A"] for s in sols), 1)


class CompactRepresentationTests(unittest.TestCase):
    def test_reduction_is_compact_and_covers_closure(self):
        order = TemporalOrder(default_domain=(0, 50))
        order.add_causal("e1", "A", "B", 1)
        order.add_causal("e2", "B", "C", 1)
        order.add_causal("e3", "A", "C", 1)
        result = order.analyze()
        red = {(r.earlier, r.later) for r in result.relations}
        # A→C 在归约中消失（被 A→B→C 覆盖），闭包中保留
        self.assertEqual(red, {("A", "B"), ("B", "C")})
        closure = {(r.earlier, r.later) for r in result.closure}
        self.assertEqual(closure, {("A", "B"), ("B", "C"), ("A", "C")})

    def test_incomparable_is_not_transitive_equivalence(self):
        # A∥B、B∥C，但 A≺C —— 证明不可比较不是传递等价类
        order = TemporalOrder(default_domain=(0, 10))
        order.add_interval("iA", "A", 0, 4)
        order.add_interval("iB", "B", 2, 6)
        order.add_interval("iC", "C", 5, 9)
        result = order.analyze()
        self.assertIn(("A", "B"), result.incomparable)
        self.assertIn(("B", "C"), result.incomparable)
        self.assertNotIn(("A", "C"), result.incomparable)
        self.assertEqual(
            [(r.earlier, r.later) for r in result.closure], [("A", "C")]
        )


class BruteForceAgreementTests(unittest.TestCase):
    def test_random_systems_match_brute_force(self):
        rng = random.Random(20260926)
        for trial in range(60):
            n = rng.randint(2, 4)
            events = [f"e{i}" for i in range(n)]
            order = TemporalOrder(default_domain=(0, 7))
            for e in events:
                lo = rng.randint(0, 6)
                hi = rng.randint(lo, 7)
                order.add_interval(f"i{e}", e, lo, hi)
            for u in events:
                for v in events:
                    if u < v and rng.random() < 0.25:
                        order.add_causal(f"g{u}{v}", u, v, rng.randint(1, 2))
            result = order.analyze(enum_cap=100000)
            self.assertFalse(result.enumeration_truncated)
            if result.feasible:
                self.assertTrue(result.enum_matches_closure, msg=f"trial {trial}")
                self.assertGreaterEqual(result.enumerated_solutions, 1)
            else:
                self.assertEqual(result.cycles[0].total_gap > 0, True)


class TimeModelTests(unittest.TestCase):
    def test_synthetic_offset_table_exact(self):
        table = UnitTable(
            {
                "tick": UnitEntry("tick", 1),
                "second": UnitEntry("second", 1),
                "minute": UnitEntry("minute", 60),
                "beat": UnitEntry("beat", 30, offset_ticks=90),
            }
        )
        self.assertEqual(table.to_ticks(3, "beat"), 180)
        self.assertEqual(table.from_ticks(180, "beat"), 3)
        with self.assertRaises(ValueError):
            table.from_ticks(100, "beat")  # 100-90 不被 30 整除
        with self.assertRaises(TypeError):
            table.to_ticks(1.5, "second")

    def test_point_duration_wallclock_distinction(self):
        p0 = TimePoint(100)
        p1 = TimePoint(130)
        delta = p1 - p0
        self.assertIsInstance(delta, Duration)
        self.assertEqual(delta.ticks, 30)
        self.assertEqual((p0 + delta), p1)
        with self.assertRaises(TypeError):
            _ = p0 + p1  # type: ignore[operator]
        epoch = WallClock(datetime(2026, 9, 26, tzinfo=timezone.utc))
        wc = WallClock.from_timepoint(TimePoint(3661), epoch)
        self.assertIn("01:01:01", str(wc))

    def test_duration_formatting_integer_only(self):
        self.assertEqual(Duration(90).format(UnitTable.metric()), "1m30s")
        self.assertEqual(Duration(-90).format(UnitTable.metric()), "-1m30s")


if __name__ == "__main__":
    unittest.main(verbosity=2)
