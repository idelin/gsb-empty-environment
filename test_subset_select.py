"""subset_select 的单元测试：python3.11 -m unittest -v"""

import itertools
import random
import unittest
from fractions import Fraction

from subset_select import Item, Reason, Status, solve


def brute_force(items, cap1, cap2):
    """暴力枚举全部子集，返回 (最优收益, 其中一个最优子集)。"""
    cap1, cap2 = Fraction(cap1), Fraction(cap2)
    best_profit, best_set = Fraction(0), ()
    for r in range(len(items) + 1):
        for combo in itertools.combinations(items, r):
            groups = [it.group for it in combo if it.group]
            if len(groups) != len(set(groups)):
                continue
            if sum((it.cost1 for it in combo), Fraction(0)) > cap1:
                continue
            if sum((it.cost2 for it in combo), Fraction(0)) > cap2:
                continue
            profit = sum((it.profit for it in combo), Fraction(0))
            if profit > best_profit:
                best_profit, best_set = profit, combo
    return best_profit, best_set


class TestBasicRules(unittest.TestCase):
    def test_both_capacities_required(self):
        # 只满足一种容量不算可行：item 超出资源2，不能被选。
        items = [Item("a", profit=5, cost1=1, cost2=100)]
        res = solve(items, capacity1=10, capacity2=10)
        self.assertEqual(res.selected, ())
        self.assertEqual(res.unselected[0].reason, Reason.EXCEEDS_CAPACITY)

    def test_zero_cost_positive_profit_selected(self):
        items = [Item("free", profit=7, cost1=0, cost2=0)]
        res = solve(items, capacity1=1, capacity2=1)
        self.assertEqual([it.name for it in res.selected], ["free"])
        self.assertEqual(res.total_profit, 7)

    def test_mutex_group_at_most_one(self):
        items = [
            Item("x", profit=5, cost1=1, cost2=1, group="g"),
            Item("y", profit=9, cost1=1, cost2=1, group="g"),
        ]
        res = solve(items, capacity1=10, capacity2=10)
        self.assertEqual([it.name for it in res.selected], ["y"])
        info = {u.item.name: u for u in res.unselected}
        self.assertEqual(info["x"].reason, Reason.GROUP_CONFLICT)

    def test_negative_profit_optional(self):
        items = [Item("bad", profit=-4, cost1=0, cost2=0)]
        res = solve(items, capacity1=5, capacity2=5)
        self.assertEqual(res.selected, ())
        self.assertEqual(res.unselected[0].reason, Reason.NEGATIVE_PROFIT)

    def test_zero_profit_no_benefit(self):
        items = [Item("meh", profit=0, cost1=1, cost2=1)]
        res = solve(items, capacity1=5, capacity2=5)
        self.assertEqual(res.selected, ())
        self.assertEqual(res.unselected[0].reason, Reason.NO_BENEFIT)

    def test_known_optimum(self):
        items = [
            Item("alpha", profit=10, cost1=4, cost2=3),
            Item("beta", profit=9, cost1=5, cost2=2, group="g1"),
            Item("gamma", profit=8, cost1=3, cost2=4, group="g1"),
            Item("delta", profit=Fraction(15, 2), cost1=Fraction(5, 2), cost2=2),
            Item("epsilon", profit=6, cost1=0, cost2=0),
        ]
        res = solve(items, capacity1=10, capacity2=8)
        self.assertEqual(res.status, Status.OPTIMAL)
        self.assertEqual(res.total_profit, 25)
        self.assertEqual({it.name for it in res.selected}, {"alpha", "beta", "epsilon"})

    def test_rational_exactness(self):
        # 有理数容量/消耗精确比较：1/3+1/3+1/3 == 1 恰好放得下三件。
        items = [Item(f"r{i}", profit=1, cost1=Fraction(1, 3), cost2=0) for i in range(3)]
        res = solve(items, capacity1=Fraction(1), capacity2=0)
        self.assertEqual(len(res.selected), 3)
        self.assertEqual(res.used1, 1)


class TestStatusAndBudget(unittest.TestCase):
    def test_optimal_status_when_unlimited(self):
        items = [Item("a", profit=3, cost1=2, cost2=2)]
        res = solve(items, 5, 5)
        self.assertEqual(res.status, Status.OPTIMAL)
        self.assertTrue(res.proven_optimal)
        self.assertTrue(res.feasible)

    def test_budget_undecided_status(self):
        items = [
            Item(f"i{i}", profit=(i * 13) % 17 + 1,
                 cost1=(i * 5) % 7 + 1, cost2=(i * 3) % 5 + 1)
            for i in range(20)
        ]
        res = solve(items, 20, 18, max_nodes=10)
        self.assertEqual(res.status, Status.BUDGET_UNDECIDED)
        self.assertFalse(res.proven_optimal)
        self.assertTrue(res.feasible)
        # 预算内可行解仍是可行解，且收益不超过已证明最优。
        self.assertLessEqual(res.used1, 20)
        self.assertLessEqual(res.used2, 18)
        full = solve(items, 20, 18)
        self.assertEqual(full.status, Status.OPTIMAL)
        self.assertLessEqual(res.total_profit, full.total_profit)

    def test_budget_undecided_reason_marked(self):
        items = [
            Item(f"i{i}", profit=(i * 13) % 17 + 1,
                 cost1=(i * 5) % 7 + 1, cost2=(i * 3) % 5 + 1)
            for i in range(20)
        ]
        res = solve(items, 20, 18, max_nodes=10)
        for info in res.unselected:
            if info.reason in (Reason.GROUP_CONFLICT, Reason.INSUFFICIENT_CAPACITY,
                               Reason.NOT_IN_OPTIMAL):
                self.assertIn("非已证明最优", info.detail)


class TestAgainstBruteForce(unittest.TestCase):
    def test_random_integer_cases(self):
        rng = random.Random(20260926)
        for case in range(60):
            n = rng.randint(1, 10)
            items = []
            for i in range(n):
                items.append(Item(
                    f"i{i}",
                    profit=rng.randint(-3, 20),
                    cost1=rng.randint(0, 8),
                    cost2=rng.randint(0, 8),
                    group=f"g{rng.randint(0, 2)}" if rng.random() < 0.4 else None,
                ))
            cap1, cap2 = rng.randint(0, 15), rng.randint(0, 15)
            res = solve(items, cap1, cap2)
            best, _ = brute_force(items, cap1, cap2)
            self.assertEqual(res.status, Status.OPTIMAL)
            self.assertEqual(res.total_profit, best, f"case {case}: {items} caps=({cap1},{cap2})")
            self.assertLessEqual(res.used1, cap1)
            self.assertLessEqual(res.used2, cap2)
            groups = [it.group for it in res.selected if it.group]
            self.assertEqual(len(groups), len(set(groups)))

    def test_random_rational_cases(self):
        rng = random.Random(7)
        for case in range(30):
            n = rng.randint(1, 8)
            items = []
            for i in range(n):
                items.append(Item(
                    f"i{i}",
                    profit=Fraction(rng.randint(1, 12), rng.randint(1, 4)),
                    cost1=Fraction(rng.randint(0, 6), rng.randint(1, 3)),
                    cost2=Fraction(rng.randint(0, 6), rng.randint(1, 3)),
                    group="g" if rng.random() < 0.25 else None,
                ))
            cap1 = Fraction(rng.randint(0, 10), rng.randint(1, 2))
            cap2 = Fraction(rng.randint(0, 10), rng.randint(1, 2))
            res = solve(items, cap1, cap2)
            best, _ = brute_force(items, cap1, cap2)
            self.assertEqual(res.total_profit, best, f"case {case}")


class TestValidation(unittest.TestCase):
    def test_duplicate_names_rejected(self):
        with self.assertRaises(ValueError):
            solve([Item("a", 1), Item("a", 2)], 1, 1)

    def test_negative_capacity_rejected(self):
        with self.assertRaises(ValueError):
            solve([], -1, 1)

    def test_negative_cost_rejected(self):
        with self.assertRaises(ValueError):
            Item("bad", profit=1, cost1=-1)

    def test_empty_inventory(self):
        res = solve([], 5, 5)
        self.assertEqual(res.status, Status.OPTIMAL)
        self.assertEqual(res.total_profit, 0)
        self.assertEqual(res.selected, ())


if __name__ == "__main__":
    unittest.main()
