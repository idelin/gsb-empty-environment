import random
import unittest
from collections import Counter
from fractions import Fraction

from cuttingstock import (
    BarCut,
    Demand,
    Inventory,
    Status,
    StockSpec,
    expected_remnants,
    run_batches,
    solve,
    verify_plan,
)

F = Fraction


class TestKerfDefinition(unittest.TestCase):
    """切缝损耗按定义计算：n 段对应 n 次切割（恰好用尽时 n-1 次）。"""

    def test_kerf_blocks_packing(self):
        # 5+5 在长度 10、切缝 1 下放不进一根（需要 5+5+1=11），只能两根
        res = solve([Demand.of(5, 2)], [StockSpec.of(10)], kerf=1)
        self.assertEqual(res.status, Status.OPTIMAL)
        self.assertEqual(len(res.bars), 2)

    def test_exact_fit_saves_last_cut(self):
        # 长度 11：5+5+1=11，恰好用尽，只切 1 次
        res = solve([Demand.of(5, 2)], [StockSpec.of(11)], kerf=1)
        self.assertEqual(len(res.bars), 1)
        self.assertEqual(res.bars[0].cuts, 1)
        self.assertEqual(res.bars[0].leftover, 0)

    def test_exact_fit_boundary(self):
        # 4+5=9、切缝 1：长度 9 放不进（9+1>9），长度 10 恰好（9+1=10）
        res = solve([Demand.of(4, 1), Demand.of(5, 1)], [StockSpec.of(9)], kerf=1)
        self.assertEqual(len(res.bars), 2)
        res2 = solve([Demand.of(4, 1), Demand.of(5, 1)], [StockSpec.of(10)], kerf=1)
        self.assertEqual(len(res2.bars), 1)
        self.assertEqual(res2.bars[0].cuts, 1)


class TestSingleAndMultiSpec(unittest.TestCase):
    def test_single_fixed_spec_special_case(self):
        # 单一固定长度是特例：10 = 4+3+3 一根，4 一根，共 2 根
        demands = [Demand.of(4, 2), Demand.of(3, 2)]
        res = solve(demands, [StockSpec.of(10, cost=1)], kerf=0)
        self.assertEqual(res.status, Status.OPTIMAL)
        self.assertEqual(len(res.bars), 2)
        self.assertEqual(res.waste, 6)
        self.assertLessEqual(res.lower_bound_waste, res.waste)
        self.assertEqual(verify_plan(res.bars, demands, [StockSpec.of(10, cost=1)]), [])

    def test_sorted_by_waste_then_cost(self):
        # 先比损耗：600 比 1000 损耗小
        res = solve(
            [Demand.of(550, 1)],
            [StockSpec.of(1000, cost=1), StockSpec.of(600, cost=99)],
            kerf=0,
        )
        self.assertEqual(res.bars[0].stock_length, 600)
        # 损耗相同再比成本
        res2 = solve(
            [Demand.of(550, 1)],
            [StockSpec.of(600, cost=8), StockSpec.of(600, cost=5)],
            kerf=0,
        )
        self.assertEqual(res2.cost, 5)

    def test_empty_demand_trivially_optimal(self):
        res = solve([], [StockSpec.of(10)], kerf=0)
        self.assertEqual(res.status, Status.OPTIMAL)
        self.assertEqual(res.bars, [])
        self.assertEqual(res.waste, 0)


class TestInventory(unittest.TestCase):
    def test_limited_inventory_infeasible(self):
        res = solve([Demand.of(60, 2)], [StockSpec.of(100, count=1)], kerf=0)
        self.assertEqual(res.status, Status.INFEASIBLE)

    def test_limited_inventory_exact_fit(self):
        demands = [Demand.of(60, 2)]
        stocks = [StockSpec.of(100, count=2)]
        res = solve(demands, stocks, kerf=0)
        self.assertEqual(res.status, Status.OPTIMAL)
        self.assertEqual(len(res.bars), 2)
        self.assertEqual(verify_plan(res.bars, demands, stocks), [])

    def test_overlong_demand_directly_infeasible(self):
        res = solve([Demand.of(700, 1)], [StockSpec.of(600)], kerf=0)
        self.assertEqual(res.status, Status.INFEASIBLE)
        self.assertEqual(res.nodes, 0)  # 直接判定，不进入搜索

    def test_inventory_cap_not_exceeded(self):
        demands = [Demand.of(7, 2), Demand.of(4, 3)]
        stocks = [StockSpec.of(12, cost=5, count=3), StockSpec.of(8, cost=3, count=2)]
        res = solve(demands, stocks, kerf=1)
        self.assertEqual(res.status, Status.OPTIMAL)
        self.assertEqual(verify_plan(res.bars, demands, stocks, kerf=1), [])
        produced = Counter(p for b in res.bars for p in b.pieces)
        self.assertEqual(produced[F(7)], 2)  # 需求逐项精确满足
        self.assertEqual(produced[F(4)], 3)  # 不多切


class TestBudget(unittest.TestCase):
    def test_budget_exhausted_with_incumbent(self):
        # 两种规格制造分支：找到可行解后预算耗尽，标记未证最优
        demands = [Demand.of(3, 2)]
        stocks = [StockSpec.of(5), StockSpec.of(4)]
        res = solve(demands, stocks, kerf=0, max_nodes=4)
        self.assertTrue(res.budget_exhausted)
        self.assertEqual(res.status, Status.FEASIBLE)
        self.assertFalse(res.proven_optimal)
        self.assertEqual(verify_plan(res.bars, demands, stocks), [])

    def test_budget_exhausted_without_incumbent(self):
        res = solve([Demand.of(3, 2)], [StockSpec.of(5), StockSpec.of(4)], kerf=0, max_nodes=1)
        self.assertTrue(res.budget_exhausted)
        self.assertEqual(res.status, Status.UNKNOWN)
        self.assertEqual(res.bars, [])

    def test_full_budget_proves_optimal(self):
        demands = [Demand.of(3, 2)]
        stocks = [StockSpec.of(5), StockSpec.of(4)]
        res = solve(demands, stocks, kerf=0)
        self.assertEqual(res.status, Status.OPTIMAL)
        self.assertEqual(res.waste, 2)


class TestRemnantReuse(unittest.TestCase):
    def test_remnant_reused_in_next_batch(self):
        inv = Inventory([StockSpec.of(1000, cost=10, count=1)])
        camp = run_batches(
            [("b1", [Demand.of(600, 1)]), ("b2", [Demand.of(400, 1)])],
            inv, kerf=0, min_remnant=300,
        )
        b1, b2 = camp.batches[0].result, camp.batches[1].result
        self.assertEqual(b1.status, Status.OPTIMAL)
        self.assertEqual(b1.reusable_remnants, (F(400),))
        self.assertEqual(b2.cost, 0)  # 第二批用余料，不耗新料
        self.assertEqual(b2.bars[0].stock_length, 400)
        self.assertEqual(b2.waste, 0)

    def test_remnant_not_double_consumed(self):
        # 仅一根 400 余料，两个批次都想要：第二批必须用新料
        inv = Inventory([StockSpec.of(400, cost=0, count=1), StockSpec.of(500, cost=3)])
        camp = run_batches(
            [("b1", [Demand.of(400, 1)]), ("b2", [Demand.of(400, 1)])],
            inv, kerf=0, min_remnant=300,
        )
        b1, b2 = camp.batches[0].result, camp.batches[1].result
        self.assertEqual(b1.bars[0].stock_length, 400)
        self.assertEqual(b2.bars[0].stock_length, 500)
        self.assertEqual(b2.cost, 3)

    def test_joint_mode_merges_demands(self):
        inv = Inventory([StockSpec.of(1000, cost=10)])
        camp = run_batches(
            [("b1", [Demand.of(600, 1)]), ("b2", [Demand.of(500, 1)])],
            inv, kerf=0, min_remnant=300, joint=True,
        )
        res = camp.batches[0].result
        self.assertEqual(res.status, Status.OPTIMAL)
        self.assertEqual(len(res.bars), 2)  # 600+500=1100 → 2 根 1000
        self.assertEqual(res.waste, 900)

    def test_remnant_accounting_matches_plan(self):
        demands = [Demand.of(1200, 2), Demand.of(700, 1)]
        stocks = [StockSpec.of(2000, cost=9)]
        res = solve(demands, stocks, kerf=3, min_remnant=400)
        self.assertEqual(res.status, Status.OPTIMAL)
        self.assertEqual(res.reusable_remnants, expected_remnants(res.bars, 400))


class TestVerification(unittest.TestCase):
    def test_verify_ok(self):
        demands = [Demand.of(4, 2), Demand.of(3, 2)]
        stocks = [StockSpec.of(10, cost=1)]
        res = solve(demands, stocks, kerf=0)
        self.assertEqual(verify_plan(res.bars, demands, stocks), [])

    def test_verify_detects_tampered_leftover(self):
        demands = [Demand.of(4, 2), Demand.of(3, 1)]
        stocks = [StockSpec.of(10, cost=1)]
        res = solve(demands, stocks, kerf=0)
        bad = [
            BarCut(b.stock_length, b.cost, b.pieces, b.cuts, b.leftover + 1)
            for b in res.bars
        ]
        self.assertTrue(verify_plan(bad, demands, stocks))

    def test_verify_detects_unmet_demand(self):
        demands = [Demand.of(4, 2), Demand.of(3, 2)]
        stocks = [StockSpec.of(10, cost=1)]
        res = solve(demands, stocks, kerf=0)
        self.assertTrue(verify_plan(res.bars, [Demand.of(4, 3), Demand.of(3, 2)], stocks))

    def test_verify_detects_inventory_overuse(self):
        demands = [Demand.of(4, 2), Demand.of(3, 2)]
        res = solve(demands, [StockSpec.of(10, cost=1)], kerf=0)
        errors = verify_plan(res.bars, demands, [StockSpec.of(10, cost=1, count=1)])
        self.assertTrue(errors)


class TestRationalAndLowerBound(unittest.TestCase):
    def test_rational_data(self):
        # 原料 5/2，需求 1/2 x4，切缝 1/4：每根最多 3 段（3/2+3/4=9/4<=5/2）
        demands = [Demand.of(F(1, 2), 4)]
        stocks = [StockSpec.of(F(5, 2))]
        res = solve(demands, stocks, kerf=F(1, 4))
        self.assertEqual(res.status, Status.OPTIMAL)
        self.assertEqual(len(res.bars), 2)
        self.assertEqual(res.waste, F(3))
        self.assertEqual(verify_plan(res.bars, demands, stocks, kerf=F(1, 4)), [])

    def test_lower_bound_never_exceeds_optimum(self):
        rng = random.Random(2026)
        checked = 0
        for _ in range(40):
            lens = rng.sample(range(3, 9), k=rng.randint(1, 3))
            demands = [Demand.of(l, rng.randint(1, 3)) for l in lens]
            stock_len = rng.randint(9, 13)
            kerf = rng.randint(0, 2)
            stocks = [StockSpec.of(stock_len)]
            res = solve(demands, stocks, kerf=kerf, max_nodes=50_000)
            if res.status is Status.OPTIMAL:
                self.assertLessEqual(res.lower_bound_waste, res.waste)
                self.assertEqual(verify_plan(res.bars, demands, stocks, kerf=kerf), [])
                checked += 1
        self.assertGreater(checked, 10)


if __name__ == "__main__":
    unittest.main()
