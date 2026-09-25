import unittest

from joinplanner.cost import CostModel
from joinplanner.planner import (JoinStep, Query, enumerate_legal_orders,
                                 plan)
from joinplanner.relations import INNER, LEFT_OUTER
from joinplanner.stats import Interval, RelStats, StatsProvider


def inner_step(rel, edges, requires=frozenset()):
    return JoinStep(rel=rel, join_type=INNER, edges=edges, requires=requires)


class EnumerationTest(unittest.TestCase):
    def test_inner_joins_reorder_freely(self):
        q = Query(base="A", steps=(
            inner_step("B", (("A", "k", "k"),)),
            inner_step("C", (("A", "k", "k"),)),
        ))
        orders = enumerate_legal_orders(list(q.steps), {"A"})
        self.assertEqual(len(orders), 2)

    def test_outer_join_is_a_barrier(self):
        q = Query(base="R", steps=(
            JoinStep(rel="S", join_type=LEFT_OUTER, edges=(("R", "k", "k"),)),
            inner_step("T", (("R", "k", "k"),)),
        ))
        orders = enumerate_legal_orders(list(q.steps), {"R"})
        # [T, S] is illegal: outer step would no longer sit at its prefix.
        self.assertEqual([[s.rel for s in o] for o in orders], [["S", "T"]])

    def test_two_outer_joins_cannot_swap(self):
        q = Query(base="R", steps=(
            JoinStep(rel="S", join_type=LEFT_OUTER, edges=(("R", "k", "k"),)),
            JoinStep(rel="T", join_type=LEFT_OUTER,
                     edges=(("R", "k", "k"), ("S", "k", "k"))),
        ))
        orders = enumerate_legal_orders(list(q.steps), {"R"})
        self.assertEqual([[s.rel for s in o] for o in orders], [["S", "T"]])

    def test_correlated_dependency(self):
        q = Query(base="A", steps=(
            inner_step("B", (("A", "k", "k"),), requires=frozenset({"C"})),
            inner_step("C", (("A", "k", "k"),)),
        ))
        orders = enumerate_legal_orders(list(q.steps), {"A"})
        self.assertEqual([[s.rel for s in o] for o in orders], [["C", "B"]])


class PlanningTest(unittest.TestCase):
    def test_triangle_low_selectivity_trap(self):
        from joinplanner.samples import sample_low_selectivity_exception
        r = sample_low_selectivity_exception()
        self.assertEqual(r["planner_order"], ["C", "B"])
        self.assertEqual(r["greedy_order"], ["B", "C"])
        self.assertTrue(r["planner_optimal"])
        self.assertEqual(r["planner_gap"], 1.0)
        self.assertGreater(r["greedy_gap"], 5.0)

    def test_unknown_stats_returns_conservative_alternatives(self):
        q = Query(base="A", steps=(
            inner_step("B", (("A", "k", "k"),)),
            inner_step("C", (("A", "k", "k"),)),
        ))
        provider = StatsProvider({
            "A": RelStats(Interval.unknown()),
            "B": RelStats(Interval.point(1000), {"k": Interval.point(1000)}),
            "C": RelStats(Interval(100, 1_000_000), {"k": Interval(10, 1000)}),
        })
        choice = plan(q, provider, CostModel())
        self.assertTrue(choice.alternatives)
        self.assertTrue(0 < choice.primary.pessimistic < float("inf"))
        # Worst-case ordering: C is a wide interval, preferring it as probe
        # side is never worse; the primary is the min-pessimistic plan.
        pess = [p.pessimistic for p in choice.ranked]
        self.assertEqual(pess, sorted(pess))

    def test_spill_influences_order_not_only_selectivity(self):
        # B fits in memory; C does not.  Both joins look identical by
        # selectivity, but ordering changes memory/spill cost.
        q = Query(base="A", steps=(
            inner_step("B", (("A", "k", "k"),)),
            inner_step("C", (("A", "k", "k"),)),
        ))
        provider = StatsProvider({
            "A": RelStats(Interval.point(100), {"k": Interval.point(100)}),
            "B": RelStats(Interval.point(1000), {"k": Interval.point(1000)}),
            "C": RelStats(Interval.point(1_000_000), {"k": Interval.point(1_000_000)}),
        })
        model = CostModel(memory_bytes=100_000, row_width=64)
        choice = plan(q, provider, model)
        self.assertEqual([s.rel for s in choice.primary.order], ["B", "C"])


if __name__ == "__main__":
    unittest.main()
