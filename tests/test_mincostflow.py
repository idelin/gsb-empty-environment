import itertools
import random
import unittest

from mincostflow import min_cost_transport


def brute_force_min_cost(supplies, demands, costs):
    """穷举所有满足行/列和的整数输运方案，返回最小成本（仅用于小实例核对）。"""
    skeys, dkeys = list(supplies), list(demands)
    n, m = len(skeys), len(dkeys)
    cells = list(itertools.product(range(n), range(m)))
    best = [None]

    def rec(idx, rows_left, cols_left, acc):
        if best[0] is not None and acc >= best[0]:
            return
        if idx == len(cells):
            if all(r == 0 for r in rows_left) and all(c == 0 for c in cols_left):
                best[0] = acc
            return
        i, j = cells[idx]
        if j == m - 1:
            amounts = (rows_left[i],)  # 行末格被行余量唯一确定
        else:
            amounts = range(0, min(rows_left[i], cols_left[j]) + 1)
        for amount in amounts:
            if amount > cols_left[j]:
                break
            rows_left[i] -= amount
            cols_left[j] -= amount
            rec(idx + 1, rows_left, cols_left,
                acc + amount * costs[(skeys[i], dkeys[j])])
            rows_left[i] += amount
            cols_left[j] += amount

    rec(0, [supplies[k] for k in skeys], [demands[k] for k in dkeys], 0)
    return best[0]


class MinCostFlowTest(unittest.TestCase):
    def test_hand_computed(self):
        supplies = {"a": 2, "b": 1}
        demands = {"x": 1, "y": 2}
        costs = {("a", "x"): 5, ("a", "y"): 1, ("b", "x"): 1, ("b", "y"): 1}
        flow, cost = min_cost_transport(supplies, demands, costs)
        self.assertEqual(cost, 3)
        self.assertEqual(flow, {("a", "y"): 2, ("b", "x"): 1})

    def test_total_mismatch_rejected(self):
        with self.assertRaises(ValueError):
            min_cost_transport({"a": 2}, {"x": 1}, {("a", "x"): 1})

    def test_against_brute_force_small_instances(self):
        rng = random.Random(20260927)
        for trial in range(60):
            n = rng.randint(1, 3)
            m = rng.randint(1, 3)
            total = rng.randint(1, 6)
            # 随机把总量切分为 k 份非负整数
            def split(k):
                weights = [rng.randint(1, 5) for _ in range(k)]
                s = sum(weights)
                parts = [total * w // s for w in weights]
                parts[-1] += total - sum(parts)
                return parts
            sup_parts = split(n)
            dem_parts = split(m)
            supplies = {f"s{i}": sup_parts[i] for i in range(n)}
            demands = {f"d{j}": dem_parts[j] for j in range(m)}
            costs = {(f"s{i}", f"d{j}"): rng.randint(0, 9)
                     for i in range(n) for j in range(m)}
            _, cost = min_cost_transport(supplies, demands, costs)
            expected = brute_force_min_cost(supplies, demands, costs)
            self.assertEqual(cost, expected,
                             f"trial {trial}: {supplies} {demands} {costs}")

    def test_flow_conservation(self):
        supplies = {"a": 3, "b": 2, "c": 1}
        demands = {"x": 2, "y": 4}
        costs = {(s, d): (i + 1) * (j + 2)
                 for i, s in enumerate(supplies) for j, d in enumerate(demands)}
        flow, cost = min_cost_transport(supplies, demands, costs)
        for s, amount in supplies.items():
            self.assertEqual(sum(v for (a, _), v in flow.items() if a == s), amount)
        for d, amount in demands.items():
            self.assertEqual(sum(v for (_, b), v in flow.items() if b == d), amount)
        self.assertEqual(cost, sum(v * costs[k] for k, v in flow.items()))


if __name__ == "__main__":
    unittest.main()
