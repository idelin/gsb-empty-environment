"""Unit tests for pareto_plans (standard-library unittest only)."""

from __future__ import annotations

import unittest
from fractions import Fraction

from pareto_plans import (
    Category,
    Combination,
    Constraints,
    Plan,
    dominates,
    enumerate_combinations,
    keep_equal_value_representatives,
)


def plans_from(rows):
    return [Plan.of(*row) for row in rows]


class DominationTests(unittest.TestCase):
    def test_better_in_one_dim_worse_in_another_cannot_dominate(self):
        a = (1, 5, 5)  # cheaper but slower than b
        b = (2, 4, 5)
        self.assertFalse(dominates(a, b))
        self.assertFalse(dominates(b, a))

    def test_strict_domination(self):
        self.assertTrue(dominates((1, 4, 5), (2, 4, 5)))
        self.assertFalse(dominates((2, 4, 5), (2, 4, 5)))  # equal is not domination


class EnumerationTests(unittest.TestCase):
    def test_incomparable_combinations_both_kept(self):
        # A is better on cost, worse on latency than B: neither may be dropped.
        plans = plans_from([
            ("A", "t", 1, 5, 5),
            ("B", "t", 2, 4, 5),
            ("C", "t", 3, 6, 6),  # dominated by A
        ])
        result = enumerate_combinations(plans)
        front_ids = {c.combo_id for c in result.optimal.combinations}
        self.assertEqual(front_ids, {("A",), ("B",)})
        self.assertEqual([c.combo_id for c in result.feasible.combinations], [("C",)])
        self.assertEqual(result.budget_undecided.total_count, 0)

    def test_equal_objectives_representative_chosen_by_id(self):
        # Every combination has identical objectives -> exactly one
        # representative, selected by the smallest combination id.
        plans = plans_from([
            ("P2", "t1", 1, 1, 1),
            ("P1", "t1", 1, 1, 1),
            ("Q1", "t2", 1, 1, 1),
        ])
        result = enumerate_combinations(plans)
        self.assertEqual(result.optimal.total_count, 1)
        rep = result.optimal.combinations[0]
        self.assertEqual(rep.combo_id, ("P1", "Q1"))
        self.assertEqual(rep.objectives, (Fraction(2), Fraction(2), Fraction(2)))

    def test_truncation_is_explicit(self):
        plans = plans_from([
            ("A", "t", 1, 9, 9),
            ("B", "t", 2, 8, 8),
            ("C", "t", 3, 7, 7),
        ])  # all three mutually incomparable -> front of size 3
        result = enumerate_combinations(plans, max_results=2)
        opt = result.optimal
        self.assertTrue(opt.truncated)
        self.assertEqual(opt.total_count, 3)
        self.assertEqual(len(opt.combinations), 2)
        self.assertEqual(opt.omitted_count, 1)
        kept_ids = [c.combo_id for c in opt.combinations]
        self.assertEqual(kept_ids, [("A",), ("B",)])  # deterministic order

    def test_max_results_zero_truncates_everything(self):
        plans = plans_from([("A", "t", 1, 1, 1)])
        result = enumerate_combinations(plans, max_results=0)
        self.assertEqual(result.optimal.combinations, [])
        self.assertTrue(result.optimal.truncated)
        self.assertEqual(result.optimal.omitted_count, 1)

    def test_categories_separated_and_all_returned_independently_feasible(self):
        plans = plans_from([
            ("A1", "t1", 2, 2, 2),
            ("A2", "t1", 9, 1, 1),
            ("A3", "t1", 20, 5, 5),
            ("B1", "t2", 2, 2, 2),
            ("B2", "t2", 9, 1, 1),
            ("B3", "t2", 2, 9, 9),
        ])
        constraints = Constraints.of(max_cost=5, max_latency=12, max_energy=12)
        result = enumerate_combinations(plans, constraints)

        self.assertEqual(result.total_combinations, 9)
        # (A3,B3) = (22, 14, 14) violates the hard latency/energy limits.
        self.assertEqual(result.hard_feasible_count, 8)

        optimal_objs = {c.objectives for c in result.optimal.combinations}
        feasible_objs = {c.objectives for c in result.feasible.combinations}
        undecided_objs = {c.objectives for c in result.budget_undecided.combinations}
        f = Fraction
        self.assertEqual(optimal_objs, {(f(4), f(4), f(4))})
        self.assertEqual(feasible_objs, {(f(4), f(11), f(11))})
        self.assertEqual(undecided_objs, {(f(11), f(3), f(3)), (f(18), f(2), f(2))})

        # Categories are disjoint.
        self.assertTrue(optimal_objs.isdisjoint(feasible_objs))
        self.assertTrue(optimal_objs.isdisjoint(undecided_objs))
        self.assertTrue(feasible_objs.isdisjoint(undecided_objs))

        # Every returned combination is independently hard-feasible, and
        # budget status matches its category.
        for cat in result.categories():
            for combo in cat.combinations:
                self.assertLessEqual(combo.latency, 12)
                self.assertLessEqual(combo.energy, 12)
                if cat.category is Category.BUDGET_UNDECIDED:
                    self.assertGreater(combo.cost, 5)
                else:
                    self.assertLessEqual(combo.cost, 5)

        # Over-budget but dominated combos (e.g. (A3,B1) = (22,7,7)) are
        # dropped and appear in no category.
        all_objs = optimal_objs | feasible_objs | undecided_objs
        self.assertNotIn((f(22), f(7), f(7)), all_objs)
        self.assertNotIn((f(29), f(6), f(6)), all_objs)

    def test_equal_value_representative_among_over_budget(self):
        # (A1,B2) and (A2,B1) both give (11,3,3) over budget: keep the
        # lexicographically smallest combination id as representative.
        plans = plans_from([
            ("A1", "t1", 2, 2, 2),
            ("A2", "t1", 9, 1, 1),
            ("B1", "t2", 2, 2, 2),
            ("B2", "t2", 9, 1, 1),
        ])
        result = enumerate_combinations(plans, Constraints.of(max_cost=5))
        undecided = result.budget_undecided.combinations
        ids = {c.combo_id for c in undecided}
        self.assertIn(("A1", "B2"), ids)
        self.assertNotIn(("A2", "B1"), ids)

    def test_rationals_are_exact(self):
        plans = [
            Plan.of("P1", "t1", "1/3", 1, Fraction(1, 2)),
            Plan.of("Q1", "t2", "1/6", 1, "0.5"),
            Plan.of("Q2", "t2", "1/2", 1, "1/4"),
        ]
        constraints = Constraints.of(max_cost="1/2")
        result = enumerate_combinations(plans, constraints)
        within = result.optimal.combinations + result.feasible.combinations
        over = result.budget_undecided.combinations
        self.assertEqual(len(within), 1)
        self.assertEqual(within[0].cost, Fraction(1, 2))  # 1/3 + 1/6 exactly
        self.assertEqual(within[0].energy, Fraction(1, 1))
        self.assertEqual(len(over), 1)
        self.assertEqual(over[0].cost, Fraction(5, 6))
        self.assertEqual(over[0].energy, Fraction(3, 4))  # incomparable: less energy

    def test_no_constraints_means_no_budget_undecided(self):
        plans = plans_from([("A", "t", 1, 1, 1), ("B", "t", 2, 2, 2)])
        result = enumerate_combinations(plans)
        self.assertEqual(result.budget_undecided.total_count, 0)
        self.assertEqual(result.optimal.total_count, 1)
        self.assertEqual(result.feasible.total_count, 1)

    def test_deterministic_across_runs_and_input_order(self):
        rows = [
            ("A", "t1", 1, 3, 2),
            ("B", "t1", 2, 2, 2),
            ("C", "t2", 1, 1, 4),
            ("D", "t2", 3, 1, 1),
        ]
        first = enumerate_combinations(plans_from(rows), max_results=10)
        second = enumerate_combinations(plans_from(list(reversed(rows))), max_results=10)
        for cat_a, cat_b in zip(first.categories(), second.categories()):
            self.assertEqual(
                [c.combo_id for c in cat_a.combinations],
                [c.combo_id for c in cat_b.combinations],
            )


class ValidationTests(unittest.TestCase):
    def test_empty_plans_rejected(self):
        with self.assertRaises(ValueError):
            enumerate_combinations([])

    def test_float_rejected_to_keep_exactness(self):
        with self.assertRaises(TypeError):
            Plan.of("X", "t", 1.5, 1, 1)

    def test_invalid_string_rejected(self):
        with self.assertRaises(ValueError):
            Plan.of("X", "t", "abc", 1, 1)

    def test_duplicate_plan_id_in_task_rejected(self):
        plans = plans_from([("A", "t", 1, 1, 1), ("A", "t", 2, 2, 2)])
        with self.assertRaises(ValueError):
            enumerate_combinations(plans)

    def test_negative_max_results_rejected(self):
        plans = plans_from([("A", "t", 1, 1, 1)])
        with self.assertRaises(ValueError):
            enumerate_combinations(plans, max_results=-1)

    def test_keep_equal_value_representatives_helper(self):
        plans = plans_from([
            ("B", "t", 1, 1, 1),
            ("A", "t", 1, 1, 1),
            ("C", "t", 2, 2, 2),
        ])
        combos = [Combination.from_plans([p]) for p in plans]
        reps = keep_equal_value_representatives(combos)
        self.assertEqual(len(reps), 2)
        rep_ids = {c.combo_id for c in reps}
        self.assertEqual(rep_ids, {("A",), ("C",)})


if __name__ == "__main__":
    unittest.main()
