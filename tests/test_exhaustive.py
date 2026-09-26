import unittest
from fractions import Fraction

from wmmf import Demand, GridStatus, grid_verify, water_fill


class TestGridVerify(unittest.TestCase):
    def setUp(self):
        self.links = {"A": 10}
        self.demands = {
            "d1": Demand("d1", weight=1, cap=10, links=("A",)),
            "d2": Demand("d2", weight=1, cap=10, links=("A",)),
        }

    def test_confirmed_optimal(self):
        alloc = water_fill(self.links, self.demands)
        result = grid_verify(
            self.links, self.demands, alloc.rates, step=1, budget=10_000
        )
        self.assertEqual(result.status, GridStatus.CONFIRMED_OPTIMAL)
        self.assertEqual(result.checked, 121)  # 11 x 11 个网格点

    def test_budget_undecided(self):
        alloc = water_fill(self.links, self.demands)
        result = grid_verify(
            self.links, self.demands, alloc.rates, step=1, budget=10
        )
        self.assertEqual(result.status, GridStatus.UNDECIDED)
        self.assertEqual(result.checked, 10)

    def test_refuted_with_counterexample(self):
        unfair = {"d1": Fraction(4), "d2": Fraction(6)}
        result = grid_verify(
            self.links, self.demands, unfair, step=1, budget=10_000
        )
        self.assertEqual(result.status, GridStatus.REFUTED)
        # 反例 (5, 5) 的排序归一向量字典序优于 (4, 6)
        self.assertIsNotNone(result.counterexample)
        vec = sorted(result.counterexample.values())
        self.assertGreater(vec, [Fraction(4), Fraction(6)])

    def test_weighted_and_multi_link(self):
        links = {"A": 2, "B": 2}
        demands = {
            "d1": Demand("d1", weight=1, cap=2, links=("A",)),
            "d2": Demand("d2", weight=1, cap=2, links=("A", "B")),
        }
        alloc = water_fill(links, demands)
        # A 被 d1、d2 共享，水位 1 时 A 饱和，两者同时冻结
        self.assertEqual(alloc.rates, {"d1": Fraction(1), "d2": Fraction(1)})
        result = grid_verify(links, demands, alloc.rates, step=1, budget=10_000)
        self.assertEqual(result.status, GridStatus.CONFIRMED_OPTIMAL)

    def test_rational_grid(self):
        links = {"A": 1}
        demands = {
            "d1": Demand("d1", weight=1, cap=1, links=("A",)),
            "d2": Demand("d2", weight=2, cap=2, links=("A",)),
        }
        alloc = water_fill(links, demands)
        # 归一水位 1/3：速率 1/3 与 2/3
        result = grid_verify(
            links, demands, alloc.rates, step=Fraction(1, 3), budget=10_000
        )
        self.assertEqual(result.status, GridStatus.CONFIRMED_OPTIMAL)


if __name__ == "__main__":
    unittest.main()
