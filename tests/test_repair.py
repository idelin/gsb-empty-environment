"""Unit tests for the solver-free plan-repair tool (Python 3.11)."""
from __future__ import annotations

import unittest
from fractions import Fraction

from repair.model import parse_number, parse_scenario
from repair.repair import Status, repair, validate
from repair.samples import SAMPLES
from repair.scheduler import (Gap, PlacementItem, compute_gaps,
                              optimize_placement)

F = Fraction


def run_sample(name, **overrides):
    data = {k: v for k, v in SAMPLES[name].items()}
    scenario = parse_scenario(data)
    if overrides:
        import dataclasses
        scenario = dataclasses.replace(scenario, **overrides)
    return repair(scenario)


def changes_by_task(result):
    return {c.task: c for c in result.changes}


class ParseNumberTests(unittest.TestCase):
    def test_int_float_str(self):
        self.assertEqual(parse_number(7), F(7))
        self.assertEqual(parse_number(1.5), F(3, 2))
        self.assertEqual(parse_number("3/2"), F(3, 2))
        self.assertEqual(parse_number("2.5"), F(5, 2))
        self.assertEqual(parse_number(" 4 "), F(4))

    def test_rejects_garbage(self):
        for bad in ("abc", True, None, [1]):
            with self.assertRaises(ValueError):
                parse_number(bad)


class ValidationTests(unittest.TestCase):
    def base(self):
        return {"now": 0, "tasks": [], "new_tasks": [], "failures": []}

    def test_overlapping_original_plan_rejected(self):
        data = self.base()
        data["tasks"] = [
            {"id": "A", "machine": "M1", "duration": 3, "start": 0},
            {"id": "B", "machine": "M1", "duration": 3, "start": 2},
        ]
        with self.assertRaisesRegex(ValueError, "overlap"):
            validate(parse_scenario(data))

    def test_unexecuted_task_in_the_past_rejected(self):
        data = self.base()
        data["now"] = 5
        data["tasks"] = [
            {"id": "A", "machine": "M1", "duration": 2, "start": 3},
        ]
        with self.assertRaisesRegex(ValueError, "not marked executed"):
            validate(parse_scenario(data))

    def test_duplicate_ids_rejected(self):
        data = self.base()
        data["tasks"] = [
            {"id": "A", "machine": "M1", "duration": 1, "start": 0},
        ]
        data["new_tasks"] = [
            {"id": "A", "machine": "M1", "duration": 1},
        ]
        with self.assertRaisesRegex(ValueError, "duplicate"):
            validate(parse_scenario(data))

    def test_nonpositive_duration_rejected(self):
        data = self.base()
        data["tasks"] = [
            {"id": "A", "machine": "M1", "duration": 0, "start": 0},
        ]
        with self.assertRaisesRegex(ValueError, "duration"):
            validate(parse_scenario(data))


class GapTests(unittest.TestCase):
    def test_merges_and_clips(self):
        gaps = compute_gaps([(F(0), F(2)), (F(1), F(4)), (F(6), F(7))], F(3))
        self.assertEqual(gaps, [Gap(F(4), F(6)), Gap(F(7), None)])

    def test_permanent_failure_has_no_tail(self):
        gaps = compute_gaps([(F(5), None)], F(0))
        self.assertEqual(gaps, [Gap(F(0), F(5))])

    def test_no_obstacles_gives_unbounded_gap(self):
        self.assertEqual(compute_gaps([], F(2)), [Gap(F(2), None)])


class PlacementTests(unittest.TestCase):
    def test_exact_placement(self):
        items = [PlacementItem("x", F(3), F(0)),
                 PlacementItem("y", F(1), F(10))]
        gaps = [Gap(F(0), F(2)), Gap(F(4), None)]
        result = optimize_placement(items, gaps, 10_000)
        self.assertTrue(result.feasible)
        self.assertTrue(result.complete)
        # x (duration 3) only fits the tail gap at 4 (cost 4); y then
        # starts exactly at its ideal 10 (cost 0).
        self.assertEqual(result.cost, F(4))
        self.assertEqual(result.starts, {"x": F(4), "y": F(10)})

    def test_infeasible_when_no_gap_is_big_enough(self):
        items = [PlacementItem("x", F(3), F(0))]
        gaps = [Gap(F(0), F(2))]
        result = optimize_placement(items, gaps, 10_000)
        self.assertFalse(result.feasible)
        self.assertTrue(result.complete)

    def test_budget_exhaustion_marks_incomplete(self):
        items = [PlacementItem("x", F(1), F(0))]
        gaps = [Gap(F(0), None)]
        result = optimize_placement(items, gaps, 0)
        self.assertFalse(result.complete)


class ResourceFailureTests(unittest.TestCase):
    def test_minimal_moves_then_cost(self):
        result = run_sample("resource_failure")
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertFalse(result.kept_original_plan)
        self.assertEqual(result.moves, 1)
        self.assertEqual(result.cost, F(9))
        changes = changes_by_task(result)
        self.assertEqual(set(changes), {"A"})
        self.assertEqual(changes["A"].old_start, F(6))
        self.assertEqual(changes["A"].new_start, F(15))

    def test_lexicographic_objective_prefers_fewer_moves(self):
        # A 2-move repair exists (A->9, B->11) with lower cost 4, but the
        # objective minimises moved tasks first, so 1 move at cost 9 wins.
        result = run_sample("resource_failure")
        self.assertEqual(result.moves, 1)
        self.assertEqual(result.cost, F(9))

    def test_unaffected_tasks_stay_in_place(self):
        result = run_sample("resource_failure")
        changed = set(changes_by_task(result))
        self.assertNotIn("B", changed)
        self.assertNotIn("C", changed)

    def test_executed_prefix_is_frozen(self):
        result = run_sample("resource_failure")
        self.assertNotIn("E", set(changes_by_task(result)))


class NewTaskTests(unittest.TestCase):
    def test_new_task_slots_in_without_moves(self):
        result = run_sample("new_task")
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.moves, 0)
        self.assertEqual(result.cost, F(4))
        changes = changes_by_task(result)
        self.assertEqual(set(changes), {"N"})
        self.assertEqual(changes["N"].kind, "scheduled")
        self.assertIsNone(changes["N"].old_start)
        self.assertEqual(changes["N"].new_start, F(4))


class FrozenConflictTests(unittest.TestCase):
    def test_executed_task_overlapping_failure_is_unrepairable(self):
        result = run_sample("frozen_conflict")
        self.assertEqual(result.status, Status.INFEASIBLE)
        self.assertTrue(result.kept_original_plan)
        self.assertEqual(result.changes, [])
        kinds = {e.kind for e in result.evidence}
        self.assertEqual(kinds, {"frozen_conflict"})
        evidence = result.evidence[0]
        self.assertEqual(evidence.details["task"], "E")
        self.assertEqual(evidence.details["failure_start"], F(5))


class BudgetTests(unittest.TestCase):
    def test_budget_undecided_keeps_original_plan(self):
        result = run_sample("budget_undecided")
        self.assertEqual(result.status, Status.BUDGET_UNDECIDED)
        self.assertTrue(result.kept_original_plan)
        self.assertEqual(result.changes, [])

    def test_limited_budget_gives_feasible_not_optimal(self):
        result = run_sample("limited_budget")
        self.assertEqual(result.status, Status.FEASIBLE)
        self.assertEqual(result.moves, 1)
        self.assertEqual(result.cost, F(11))  # bound, not proven optimal

    def test_full_budget_proves_optimal_and_improves_cost(self):
        result = run_sample("limited_budget", subset_budget=100_000)
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.moves, 1)
        self.assertEqual(result.cost, F(3))
        changes = changes_by_task(result)
        self.assertEqual(changes["C"].new_start, F(5))
        self.assertEqual(changes["N"].new_start, F(2))

    def test_placement_budget_can_trigger_undecided(self):
        result = run_sample("resource_failure", placement_budget=0)
        self.assertEqual(result.status, Status.BUDGET_UNDECIDED)
        self.assertTrue(result.kept_original_plan)


class InfeasibleTests(unittest.TestCase):
    def test_permanent_shutdown_without_capacity(self):
        result = run_sample("permanent_shutdown")
        self.assertEqual(result.status, Status.INFEASIBLE)
        self.assertTrue(result.kept_original_plan)
        evidence = result.evidence[0]
        self.assertEqual(evidence.kind, "insufficient_capacity")
        self.assertEqual(evidence.details["displaced_tasks"], ["B"])
        self.assertEqual(evidence.details["required_duration"], F(3))
        self.assertEqual(evidence.details["remaining_capacity"], F(4))


class RationalTests(unittest.TestCase):
    def test_rational_data_exact_arithmetic(self):
        result = run_sample("rationals")
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.moves, 1)
        self.assertEqual(result.cost, F(1, 2))
        changes = changes_by_task(result)
        self.assertEqual(changes["A"].new_start, F(1))
        self.assertEqual(changes["A"].old_start, F(3, 2))


class DecompositionTests(unittest.TestCase):
    def test_machines_are_independent(self):
        data = {
            "now": 0,
            "tasks": [
                {"id": "A", "machine": "M1", "duration": 2, "start": 1},
                {"id": "X", "machine": "M2", "duration": 2, "start": 0},
                {"id": "Y", "machine": "M2", "duration": 2, "start": 2},
            ],
            "new_tasks": [],
            "failures": [{"machine": "M1", "start": 1, "end": 2}],
        }
        result = repair(parse_scenario(data))
        self.assertEqual(result.status, Status.OPTIMAL)
        changes = changes_by_task(result)
        self.assertEqual(set(changes), {"A"})  # M2 untouched
        self.assertEqual(result.machines["M2"].moves, 0)
        self.assertEqual(result.machines["M2"].cost, F(0))

    def test_new_task_on_empty_machine(self):
        data = {
            "now": 3,
            "tasks": [],
            "new_tasks": [{"id": "N", "machine": "M9", "duration": 2}],
            "failures": [],
        }
        result = repair(parse_scenario(data))
        self.assertEqual(result.status, Status.OPTIMAL)
        changes = changes_by_task(result)
        self.assertEqual(changes["N"].new_start, F(3))  # not before now
        self.assertEqual(result.cost, F(3))

    def test_nothing_to_do(self):
        data = {
            "now": 0,
            "tasks": [{"id": "A", "machine": "M1", "duration": 2,
                       "start": 0}],
            "new_tasks": [],
            "failures": [],
        }
        result = repair(parse_scenario(data))
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.moves, 0)
        self.assertEqual(result.cost, F(0))
        self.assertEqual(result.changes, [])


if __name__ == "__main__":
    unittest.main()
