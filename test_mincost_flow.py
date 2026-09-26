"""mincost_flow 的单元测试（仅使用标准库 unittest）。

运行：python3.11 -m unittest -v test_mincost_flow
"""

from fractions import Fraction

import unittest

from mincost_flow import (
    BudgetVerdict,
    Feasibility,
    MinCostFlow,
    Optimality,
    Z,
    solve_min_cost_flow,
)


def flow_map(result):
    return {f.label: f.flow for f in result.flows}


class TestInputNormalization(unittest.TestCase):
    def test_Z_normalizes_int_string_fraction(self):
        self.assertEqual(Z(3), Fraction(3))
        self.assertEqual(Z("2/7"), Fraction(2, 7))
        self.assertEqual(Z(Fraction(1, 2)), Fraction(1, 2))

    def test_Z_rejects_bool_and_float(self):
        with self.assertRaises(TypeError):
            Z(True)
        with self.assertRaises(TypeError):
            Z(1.5)


class TestSimpleMinCost(unittest.TestCase):
    def test_single_source_sink_picks_cheap_edge(self):
        mcf = MinCostFlow()
        mcf.add_node("s", 4)
        mcf.add_node("t", -4)
        mcf.add_edge("s", "t", 0, 3, 1, "cheap")
        mcf.add_edge("s", "t", 0, 5, 7, "pricey")
        r = mcf.solve()
        self.assertIs(r.feasibility, Feasibility.FEASIBLE)
        self.assertIs(r.optimality, Optimality.OPTIMAL)
        self.assertEqual(flow_map(r), {"cheap": 3, "pricey": 1})
        self.assertEqual(r.cost, Fraction(10))
        self.assertTrue(r.is_integral())

    def test_zero_supply_zero_demand(self):
        r = solve_min_cost_flow({}, [])
        self.assertIs(r.feasibility, Feasibility.FEASIBLE)
        self.assertEqual(r.cost, 0)


class TestSupplyDemandMismatch(unittest.TestCase):
    def test_total_supply_not_equal_demand_rejected(self):
        mcf = MinCostFlow()
        mcf.add_node("s", 5)
        mcf.add_node("t", -3)
        mcf.add_edge("s", "t", 0, 100, 0, "e")
        r = mcf.solve()
        self.assertIs(r.feasibility, Feasibility.INFEASIBLE)
        self.assertIs(r.optimality, Optimality.NOT_APPLICABLE)
        self.assertIs(r.budget, BudgetVerdict.UNDECIDED)
        self.assertIsNotNone(r.evidence)
        self.assertEqual(r.evidence.reason, "supply_demand_mismatch")
        self.assertEqual(r.evidence.total_supply, 5)
        self.assertEqual(r.evidence.total_demand, 3)

    def test_multiple_supplies_aggregate_check(self):
        mcf = MinCostFlow()
        mcf.add_node("s1", 2)
        mcf.add_node("s2", 2)
        mcf.add_node("t", -3)
        mcf.add_edge("s1", "t", 0, 10, 0)
        mcf.add_edge("s2", "t", 0, 10, 0)
        r = mcf.solve()
        self.assertIs(r.feasibility, Feasibility.INFEASIBLE)
        self.assertEqual(r.evidence.reason, "supply_demand_mismatch")


class TestBoundsViolation(unittest.TestCase):
    def test_lower_greater_than_upper(self):
        mcf = MinCostFlow()
        mcf.add_node("s", 1)
        mcf.add_node("t", -1)
        mcf.add_edge("s", "t", 4, 3, 0, "bad")
        r = mcf.solve()
        self.assertIs(r.feasibility, Feasibility.INFEASIBLE)
        self.assertEqual(r.evidence.reason, "bounds_violation")

    def test_negative_lower_bound_rejected(self):
        mcf = MinCostFlow()
        mcf.add_node("s", 1)
        mcf.add_node("t", -1)
        mcf.add_edge("s", "t", -1, 3, 0)
        r = mcf.solve()
        self.assertIs(r.feasibility, Feasibility.INFEASIBLE)
        self.assertEqual(r.evidence.reason, "bounds_violation")


class TestLowerBoundPreflow(unittest.TestCase):
    def test_lower_bound_forced_and_balance_shifted(self):
        # 下界 2 强制占用；s 供给 3，故只需再送 1 单位，走便宜边
        mcf = MinCostFlow()
        mcf.add_node("s", 3)
        mcf.add_node("t", -3)
        mcf.add_edge("s", "t", 2, 5, 10, "forced")
        mcf.add_edge("s", "t", 0, 5, 1, "flex")
        r = mcf.solve()
        self.assertIs(r.feasibility, Feasibility.FEASIBLE)
        self.assertEqual(flow_map(r)["forced"], 2)
        self.assertEqual(flow_map(r)["flex"], 1)
        self.assertEqual(r.cost, Fraction(21))

    def test_lower_bound_creates_supply_at_head(self):
        # a -> t 下界 2：给 t 预置 2 单位流入，t 只需再收 1；
        # 同时 a 因下界产生 2 单位净流出需求，必须由 s 供给
        mcf = MinCostFlow()
        mcf.add_node("s", 3)
        mcf.add_node("a", 0)
        mcf.add_node("t", -3)
        mcf.add_edge("a", "t", 2, 4, 1, "at")
        mcf.add_edge("s", "a", 0, 4, 1, "sa")
        mcf.add_edge("s", "t", 0, 4, 100, "st")
        r = mcf.solve()
        self.assertIs(r.feasibility, Feasibility.FEASIBLE)
        flows = flow_map(r)
        self.assertEqual(flows["at"], 3)   # 下界 2 + 再走 1
        self.assertEqual(flows["sa"], 3)
        self.assertEqual(flows["st"], 0)
        self.assertEqual(r.cost, Fraction(6))

    def test_lower_bound_exceeding_supply_is_infeasible(self):
        # 下界强制 5 单位从 s 流出，但 s 只有 2 单位供给
        mcf = MinCostFlow()
        mcf.add_node("s", 2)
        mcf.add_node("a", 0)
        mcf.add_node("t", -2)
        mcf.add_edge("s", "a", 5, 8, 0, "too_high")
        mcf.add_edge("a", "t", 0, 10, 0, "out")
        r = mcf.solve()
        self.assertIs(r.feasibility, Feasibility.INFEASIBLE)
        self.assertEqual(r.evidence.reason, "cut")


class TestCutEvidence(unittest.TestCase):
    def test_capacity_bottleneck_evidence(self):
        mcf = MinCostFlow()
        mcf.add_node("s", 3)
        mcf.add_node("t", -3)
        mcf.add_edge("s", "t", 0, 2, 1, "bridge")
        r = mcf.solve()
        self.assertIs(r.feasibility, Feasibility.INFEASIBLE)
        ev = r.evidence
        self.assertEqual(ev.reason, "cut")
        self.assertIn("s", ev.reachable)
        self.assertNotIn("t", ev.reachable)
        self.assertEqual(ev.cut_capacity, 2)
        self.assertEqual(ev.required, 3)
        labels = [item[0] for item in ev.blocked_forward]
        self.assertIn("bridge", labels)

    def test_structural_infeasibility_disconnected_demand(self):
        mcf = MinCostFlow()
        mcf.add_node("s", 2)
        mcf.add_node("t1", -1)
        mcf.add_node("t2", -1)  # t2 无任何入边
        mcf.add_edge("s", "t1", 0, 5, 0, "ok")
        r = mcf.solve()
        self.assertIs(r.feasibility, Feasibility.INFEASIBLE)
        self.assertEqual(r.evidence.reason, "cut")
        self.assertNotIn("t2", r.evidence.reachable)


class TestNegativeCycles(unittest.TestCase):
    def _network(self):
        mcf = MinCostFlow()
        mcf.add_node("s", 2)
        mcf.add_node("u", 0)
        mcf.add_node("v", 0)
        mcf.add_node("t", -2)
        mcf.add_edge("s", "u", 0, 2, 10, "su")
        mcf.add_edge("s", "v", 0, 2, 0, "sv")
        mcf.add_edge("u", "t", 0, 2, 0, "ut")
        mcf.add_edge("v", "t", 0, 2, 10, "vt")
        mcf.add_edge("u", "v", 0, 2, -6, "uv")
        mcf.add_edge("v", "u", 0, 2, 5, "vu")
        return mcf

    def test_negative_cycle_canceled_to_optimum(self):
        r = self._network().solve()
        self.assertIs(r.feasibility, Feasibility.FEASIBLE)
        self.assertIs(r.optimality, Optimality.OPTIMAL)
        self.assertGreaterEqual(r.negative_cycles_canceled, 1)
        self.assertEqual(r.cost, Fraction(10))

    def test_negative_cycle_is_bounded_by_capacity(self):
        # 负环 u->v(-6)->u(+5) 增广量受 vu 上界 2 限制，不会无限循环
        r = self._network().solve()
        flows = flow_map(r)
        for name, val in flows.items():
            self.assertGreaterEqual(val, 0)
        self.assertLessEqual(flows["vu"], 2)
        self.assertLessEqual(flows["uv"], 2)

    def test_pure_circulation_negative_cycle(self):
        # 纯环流：无供需，负环应被完全消除到零费用
        mcf = MinCostFlow()
        mcf.add_node("a", 0)
        mcf.add_node("b", 0)
        mcf.add_edge("a", "b", 0, 3, -5, "ab")
        mcf.add_edge("b", "a", 0, 3, 2, "ba")
        r = mcf.solve()
        self.assertIs(r.feasibility, Feasibility.FEASIBLE)
        self.assertIs(r.optimality, Optimality.OPTIMAL)
        self.assertEqual(r.cost, Fraction(-9))  # 环流量 3，每圈 -3
        self.assertGreaterEqual(r.negative_cycles_canceled, 1)

    def test_negative_cost_edge_but_no_negative_cycle(self):
        # 单条负费用边无环可成：直接在最小费用流中用满
        mcf = MinCostFlow()
        mcf.add_node("s", 2)
        mcf.add_node("t", -2)
        mcf.add_edge("s", "t", 0, 5, -4, "neg")
        r = mcf.solve()
        self.assertEqual(r.cost, Fraction(-8))
        self.assertEqual(r.negative_cycles_canceled, 0)


class TestBudget(unittest.TestCase):
    def _solve(self, budget):
        mcf = MinCostFlow()
        mcf.add_node("s", 2)
        mcf.add_node("u", 0)
        mcf.add_node("v", 0)
        mcf.add_node("t", -2)
        mcf.add_edge("s", "u", 0, 2, 10, "su")
        mcf.add_edge("s", "v", 0, 2, 0, "sv")
        mcf.add_edge("u", "t", 0, 2, 0, "ut")
        mcf.add_edge("v", "t", 0, 2, 10, "vt")
        mcf.add_edge("u", "v", 0, 2, -6, "uv")
        mcf.add_edge("v", "u", 0, 2, 5, "vu")
        return mcf.solve(budget=budget)

    def test_within_over_and_undecided(self):
        self.assertIs(self._solve(12).budget, BudgetVerdict.WITHIN_BUDGET)
        self.assertIs(self._solve(8).budget, BudgetVerdict.OVER_BUDGET)
        self.assertIs(self._solve(Fraction(10)).budget, BudgetVerdict.WITHIN_BUDGET)
        self.assertIs(self._solve(None).budget, BudgetVerdict.UNDECIDED)

    def test_budget_undecided_when_optimality_not_proven(self):
        # 只允许 0 次消圈：有可行流但未证最优，预算必须保持 UNDECIDED
        mcf = MinCostFlow(max_cycle_iterations=0)
        mcf.add_node("s", 2)
        mcf.add_node("u", 0)
        mcf.add_node("v", 0)
        mcf.add_node("t", -2)
        mcf.add_edge("s", "u", 0, 2, 10, "su")
        mcf.add_edge("s", "v", 0, 2, 0, "sv")
        mcf.add_edge("u", "t", 0, 2, 0, "ut")
        mcf.add_edge("v", "t", 0, 2, 10, "vt")
        mcf.add_edge("u", "v", 0, 2, -6, "uv")
        mcf.add_edge("v", "u", 0, 2, 5, "vu")
        r = mcf.solve(budget=100)
        self.assertIs(r.feasibility, Feasibility.FEASIBLE)
        self.assertIs(r.optimality, Optimality.NOT_PROVEN)
        self.assertIs(r.budget, BudgetVerdict.UNDECIDED)


class TestRationalData(unittest.TestCase):
    def test_fractional_costs_exact(self):
        mcf = MinCostFlow()
        mcf.add_node("s", 2)
        mcf.add_node("t", -2)
        mcf.add_edge("s", "t", 0, 2, Fraction(1, 3), "a")
        mcf.add_edge("s", "t", 0, 2, Fraction(1, 2), "b")
        r = mcf.solve(require_integral=False)
        self.assertEqual(r.cost, Fraction(2, 3))

    def test_fractional_capacities_produce_fractional_flow(self):
        mcf = MinCostFlow()
        mcf.add_node("s", Fraction(1, 2))
        mcf.add_node("t", Fraction(-1, 2))
        mcf.add_edge("s", "t", 0, 1, Fraction(1, 4), "e")
        r = mcf.solve(require_integral=False)
        self.assertIs(r.feasibility, Feasibility.FEASIBLE)
        self.assertEqual(flow_map(r)["e"], Fraction(1, 2))
        self.assertEqual(r.cost, Fraction(1, 8))
        self.assertFalse(r.is_integral())


if __name__ == "__main__":
    unittest.main(verbosity=2)
