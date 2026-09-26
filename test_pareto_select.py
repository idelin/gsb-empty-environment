"""pareto_select 的单元测试（Python 3.11，仅标准库 unittest）。"""

import unittest
from fractions import Fraction

from pareto_select import (
    Budget,
    Combination,
    Plan,
    Task,
    dominates,
    enumerate_combinations,
    pareto_representatives,
    select_plans,
)


def make_task(task_id, vectors):
    """vectors: [(plan_suffix, cost, latency, energy), ...]"""
    plans = tuple(
        Plan.of(f"{task_id}{suffix}", cost, latency, energy)
        for suffix, cost, latency, energy in vectors
    )
    return Task(task_id, plans)


class DominanceTest(unittest.TestCase):
    def combo(self, combo_id, cost, latency, energy):
        return Combination(combo_id, (("T", f"P{combo_id}"),),
                           Fraction(cost), Fraction(latency), Fraction(energy))

    def test_strict_dominance(self):
        a = self.combo(0, 1, 1, 1)
        b = self.combo(1, 2, 2, 2)
        self.assertTrue(dominates(a, b))
        self.assertFalse(dominates(b, a))

    def test_weak_dominance_with_one_equal_dim(self):
        a = self.combo(0, 1, 2, 2)
        b = self.combo(1, 1, 3, 3)
        self.assertTrue(dominates(a, b))

    def test_tradeoff_dims_cannot_dominate_each_other(self):
        # a 成本更优但时延更差：一维更好一维更差，不能互删
        a = self.combo(0, 1, 5, 3)
        b = self.combo(1, 2, 4, 3)
        self.assertFalse(dominates(a, b))
        self.assertFalse(dominates(b, a))

    def test_identical_vectors_do_not_dominate(self):
        a = self.combo(0, 2, 2, 2)
        b = self.combo(1, 2, 2, 2)
        self.assertFalse(dominates(a, b))
        self.assertFalse(dominates(b, a))


class EnumerationTest(unittest.TestCase):
    def test_cartesian_product_and_sums(self):
        tasks = [
            make_task("T1", [("A", 1, 2, 3), ("B", 4, 5, 6)]),
            make_task("T2", [("A", 10, 20, 30), ("B", 40, 50, 60), ("C", 1, 1, 1)]),
        ]
        combos = list(enumerate_combinations(tasks))
        self.assertEqual(len(combos), 6)
        self.assertEqual([c.combo_id for c in combos], list(range(6)))
        first = combos[0]
        self.assertEqual(first.vector(), (Fraction(11), Fraction(22), Fraction(33)))
        self.assertEqual(first.choices, (("T1", "T1A"), ("T2", "T2A")))

    def test_empty_tasks_yield_nothing(self):
        self.assertEqual(list(enumerate_combinations([])), [])

    def test_fractional_objectives_are_exact(self):
        tasks = [make_task("T1", [("A", Fraction(1, 3), "2/3", 1)]),
                 make_task("T2", [("A", Fraction(1, 3), "1/3", 1)])]
        (combo,) = enumerate_combinations(tasks)
        self.assertEqual(combo.cost, Fraction(2, 3))
        self.assertEqual(combo.latency, Fraction(1))
        self.assertIsInstance(combo.cost, Fraction)


class ParetoRepresentativeTest(unittest.TestCase):
    def test_identical_vectors_keep_smallest_id(self):
        tasks = [
            make_task("T1", [("A", 1, 1, 1), ("B", 1, 1, 1), ("C", 2, 2, 2)]),
            make_task("T2", [("A", 1, 1, 1)]),
        ]
        # T1A+T2A 与 T1B+T2A 目标向量均为 (2,2,2)（且为最优值），保留 id 较小者
        feasible = list(enumerate_combinations(tasks))
        front = pareto_representatives(feasible)
        vectors = [c.vector() for c in front]
        self.assertEqual(vectors, [(Fraction(2), Fraction(2), Fraction(2))])
        rep = front[0]
        self.assertEqual(rep.combo_id, 0)
        self.assertEqual(rep.label(), "T1A+T2A")

    def test_dominated_removed_tradeoff_kept(self):
        tasks = [make_task("T1", [
            ("A", 1, 5, 3),   # 与 B 互不支配（成本优、时延差）
            ("B", 2, 4, 3),   # 与 A 互不支配
            ("C", 3, 6, 4),   # 被 A 和 B 同时支配
        ])]
        feasible = list(enumerate_combinations(tasks))
        front = pareto_representatives(feasible)
        self.assertEqual({c.label() for c in front}, {"T1A", "T1B"})


class SelectPlansTest(unittest.TestCase):
    def setUp(self):
        self.tasks = [
            make_task("T1", [("A", 4, 6, 3), ("B", 6, 3, 5), ("C", 3, 8, 2)]),
            make_task("T2", [("A", 5, 4, 6), ("B", 2, 7, Fraction(7, 2)), ("C", 7, 2, 4)]),
            make_task("T3", [("A", 3, 5, 5), ("B", 5, 3, 3)]),
        ]
        self.budget = Budget.of(max_cost=14, max_latency=16, max_energy=14)

    def test_groups_are_partitioned(self):
        result = select_plans(self.tasks, self.budget)
        total = len(result.feasible) + len(result.over_budget)
        self.assertEqual(total, 3 * 3 * 2)
        feasible_ids = {c.combo_id for c in result.feasible}
        over_ids = {c.combo_id for c in result.over_budget}
        self.assertFalse(feasible_ids & over_ids)
        # 超预算组合确实违反预算
        for c in result.over_budget:
            self.assertFalse(self.budget.admits(c.cost, c.latency, c.energy))

    def test_all_returned_plans_independently_feasible(self):
        result = select_plans(self.tasks, self.budget)
        self.assertTrue(result.optimal)
        for c in result.optimal:
            self.assertTrue(self.budget.admits(c.cost, c.latency, c.energy))
        for c in result.feasible:
            self.assertTrue(self.budget.admits(c.cost, c.latency, c.energy))

    def test_optimal_is_subset_of_feasible_and_nondominated(self):
        result = select_plans(self.tasks, self.budget)
        feasible_ids = {c.combo_id for c in result.feasible}
        for c in result.optimal:
            self.assertIn(c.combo_id, feasible_ids)
        opt = list(result.optimal)
        for i, a in enumerate(opt):
            for j, b in enumerate(opt):
                if i != j:
                    self.assertFalse(dominates(a, b))

    def test_truncation_is_explicit(self):
        full = select_plans(self.tasks, self.budget)
        self.assertFalse(full.truncated)
        self.assertEqual(full.omitted, 0)
        limited = select_plans(self.tasks, self.budget, max_results=2)
        self.assertTrue(limited.truncated)
        self.assertEqual(len(limited.optimal), 2)
        self.assertEqual(limited.omitted, len(full.optimal) - 2)
        # 截断保留排序后的前两个
        self.assertEqual([c.combo_id for c in limited.optimal],
                         [c.combo_id for c in full.optimal[:2]])

    def test_no_budget_means_everything_feasible(self):
        result = select_plans(self.tasks, Budget.of())
        self.assertEqual(len(result.over_budget), 0)
        self.assertEqual(len(result.feasible), 18)

    def test_partial_budget_dimension(self):
        result = select_plans(self.tasks, Budget.of(max_cost=8))
        for c in result.feasible:
            self.assertLessEqual(c.cost, 8)
        self.assertTrue(result.over_budget)

    def test_invalid_max_results_rejected(self):
        with self.assertRaises(ValueError):
            select_plans(self.tasks, self.budget, max_results=0)

    def test_task_validation(self):
        with self.assertRaises(ValueError):
            Task("T1", ())
        with self.assertRaises(ValueError):
            make_task("T1", [("A", 1, 1, 1), ("A", 2, 2, 2)])

    def test_float_objectives_rejected(self):
        with self.assertRaises(TypeError):
            Plan.of("P", 1.5, 1, 1)


if __name__ == "__main__":
    unittest.main()
