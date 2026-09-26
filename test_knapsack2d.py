import random
import unittest
from fractions import Fraction

from knapsack2d import Item, Status, solve, to_fraction


def brute_force_best_profit(items, cap1, cap2):
    """暴力枚举所有合法子集，作为精确求解器的对照。"""
    best = 0
    n = len(items)
    for mask in range(1 << n):
        seen_groups = set()
        w1 = w2 = Fraction(0)
        profit = 0
        feasible = True
        for i in range(n):
            if not (mask >> i) & 1:
                continue
            it = items[i]
            if it.group is not None:
                if it.group in seen_groups:
                    feasible = False
                    break
                seen_groups.add(it.group)
            w1 += to_fraction(it.weight1)
            w2 += to_fraction(it.weight2)
            profit += it.profit
        if feasible and w1 <= cap1 and w2 <= cap2:
            best = max(best, profit)
    return best


class SolverTest(unittest.TestCase):
    def test_both_capacities_must_hold(self):
        items = [
            Item("too_wide", profit=100, weight1=6, weight2=1),  # 超资源1
            Item("too_tall", profit=100, weight1=1, weight2=6),  # 超资源2
            Item("fits", profit=5, weight1=5, weight2=5),
        ]
        res = solve(items, 5, 5)
        self.assertIs(res.status, Status.OPTIMAL)
        self.assertEqual(res.selected, ["fits"])
        self.assertEqual(res.total_profit, 5)
        self.assertIn("资源1", res.unselected_reasons["too_wide"])
        self.assertIn("资源2", res.unselected_reasons["too_tall"])

    def test_mutex_group_at_most_one(self):
        items = [
            Item("g1", profit=8, weight1=2, weight2=2, group="g"),
            Item("g2", profit=9, weight1=2, weight2=2, group="g"),
            Item("solo", profit=1, weight1=1, weight2=1),
        ]
        res = solve(items, 10, 10)
        chosen = [n for n in res.selected if n in ("g1", "g2")]
        self.assertEqual(chosen, ["g2"])
        self.assertIn("互斥组", res.unselected_reasons["g1"])
        self.assertEqual(res.total_profit, 10)

    def test_zero_cost_positive_profit_selected(self):
        items = [Item("free", profit=3, weight1=0, weight2=0)]
        res = solve(items, 0, 0)
        self.assertEqual(res.selected, ["free"])
        self.assertEqual(res.total_profit, 3)

    def test_negative_profit_skipped(self):
        items = [
            Item("bad", profit=-2, weight1=0, weight2=0),
            Item("good", profit=4, weight1=1, weight2=1),
        ]
        res = solve(items, 5, 5)
        self.assertEqual(res.selected, ["good"])
        self.assertIn("收益非正", res.unselected_reasons["bad"])

    def test_rational_weights(self):
        items = [Item(f"r{i}", profit=2, weight1=Fraction(1, 3), weight2="1/4") for i in range(3)]
        items.append(Item("heavy", profit=100, weight1=(3, 2), weight2=1))
        res = solve(items, 1, 1)
        self.assertIs(res.status, Status.OPTIMAL)
        self.assertEqual(res.total_profit, 6)
        self.assertEqual(sorted(res.selected), ["r0", "r1", "r2"])
        self.assertIn("资源1", res.unselected_reasons["heavy"])

    def test_budget_exhausted_is_feasible_not_optimal(self):
        many = [
            Item(f"x{i}", profit=(i * 7) % 13 + 1, weight1=(i * 3) % 7 + 1, weight2=(i * 5) % 6 + 1)
            for i in range(24)
        ]
        res = solve(many, 40, 30, state_budget=50)
        self.assertIs(res.status, Status.FEASIBLE)
        self.assertIsNotNone(res.total_profit)
        used1, used2 = res.used
        self.assertLessEqual(used1, 40)
        self.assertLessEqual(used2, 30)
        groups = [it.group for it in many if it.group and it.group in res.selected]
        self.assertEqual(len(groups), len(set(groups)))

    def test_zero_budget_is_undecided(self):
        res = solve([Item("a", profit=1, weight1=1, weight2=1)], 5, 5, state_budget=0)
        self.assertIs(res.status, Status.UNDECIDED)
        self.assertIsNone(res.total_profit)
        self.assertEqual(res.selected, [])

    def test_empty_item_list(self):
        res = solve([], 5, 5)
        self.assertIs(res.status, Status.OPTIMAL)
        self.assertEqual(res.total_profit, 0)
        self.assertEqual(res.selected, [])

    def test_invalid_input_rejected(self):
        with self.assertRaises(ValueError):
            solve([Item("a", profit=1)], -1, 5)
        with self.assertRaises(ValueError):
            solve([Item("a", profit=Fraction(1, 2))], 5, 5)
        with self.assertRaises(ValueError):
            solve([Item("a", profit=1, weight1=-1)], 5, 5)

    def test_matches_brute_force_on_random_instances(self):
        rng = random.Random(20260926)
        for trial in range(40):
            n = rng.randint(1, 9)
            items = []
            for i in range(n):
                group = f"g{rng.randint(0, 2)}" if rng.random() < 0.35 else None
                if rng.random() < 0.3:
                    w1 = Fraction(rng.randint(0, 6), rng.choice([1, 2, 3]))
                else:
                    w1 = rng.randint(0, 5)
                items.append(Item(
                    f"i{i}",
                    profit=rng.randint(-3, 10),
                    weight1=w1,
                    weight2=rng.randint(0, 5),
                    group=group,
                ))
            cap1 = rng.randint(0, 8)
            cap2 = rng.randint(0, 8)
            res = solve(items, cap1, cap2)
            self.assertIs(res.status, Status.OPTIMAL, msg=f"trial {trial}")
            expected = brute_force_best_profit(items, cap1, cap2)
            self.assertEqual(res.total_profit, expected, msg=f"trial {trial}")
            # 所选子集本身必须可行
            chosen = {it.name: it for it in items}
            w1 = sum(to_fraction(chosen[s].weight1) for s in res.selected)
            w2 = sum(to_fraction(chosen[s].weight2) for s in res.selected)
            self.assertLessEqual(w1, cap1)
            self.assertLessEqual(w2, cap2)
            groups = [chosen[s].group for s in res.selected if chosen[s].group]
            self.assertEqual(len(groups), len(set(groups)))


if __name__ == "__main__":
    unittest.main()
