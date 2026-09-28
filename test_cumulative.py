"""Unit tests for cumulative.py (standard-library unittest)."""

import itertools
import random
import unittest

from cumulative import (
    Overload,
    Task,
    check_solution,
    compulsory_interval,
    domain,
    propagate,
    solve,
)


def brute_force_solutions(tasks, capacity):
    """Enumerate every assignment and keep those passing the checker."""
    names = [t.name for t in tasks]
    solutions = []
    for combo in itertools.product(*(sorted(t.starts) for t in tasks)):
        assignment = dict(zip(names, combo))
        if check_solution(tasks, capacity, assignment):
            solutions.append(assignment)
    return solutions


class ValidationTest(unittest.TestCase):
    def test_single_task_demand_exceeds_capacity_fails_immediately(self):
        tasks = [Task("big", domain(0, 3), duration=2, demand=5)]
        with self.assertRaises(ValueError):
            solve(tasks, 3)

    def test_zero_duration_task_exempt_from_demand_check(self):
        # Zero duration never occupies the resource, so demand is moot.
        tasks = [Task("z", domain(0, 2), duration=0, demand=100)]
        self.assertIsNotNone(solve(tasks, 1))

    def test_duplicate_names_rejected(self):
        tasks = [Task("a", domain(0, 1), 1, 1), Task("a", domain(0, 1), 1, 1)]
        with self.assertRaises(ValueError):
            solve(tasks, 1)

    def test_invalid_task_fields_rejected(self):
        with self.assertRaises(ValueError):
            Task("bad", frozenset(), 1, 1)
        with self.assertRaises(ValueError):
            Task("bad", domain(0, 1), -1, 1)
        with self.assertRaises(ValueError):
            Task("bad", domain(0, 1), 1, -1)


class CompulsoryIntervalTest(unittest.TestCase):
    def test_forced_interval(self):
        task = Task("t", domain(0, 2), duration=4, demand=1)
        self.assertEqual(compulsory_interval(task), (2, 4))

    def test_no_forced_interval(self):
        task = Task("t", domain(0, 5), duration=3, demand=1)
        self.assertIsNone(compulsory_interval(task))

    def test_zero_duration_never_occupies(self):
        task = Task("t", domain(0, 2), duration=0, demand=5)
        self.assertIsNone(compulsory_interval(task))


class PropagationTest(unittest.TestCase):
    def test_prunes_values_overlapping_compulsory_interval(self):
        tasks = [
            Task("a", frozenset({0}), duration=2, demand=2),
            Task("b", domain(0, 3), duration=2, demand=1),
        ]
        pruned = propagate(tasks, 2)
        self.assertEqual(pruned[0], frozenset({0}))
        self.assertEqual(pruned[1], frozenset({2, 3}))

    def test_overload_raises_with_time_window_evidence(self):
        tasks = [
            Task("x", domain(0, 1), duration=2, demand=1),
            Task("y", domain(0, 1), duration=2, demand=1),
        ]
        with self.assertRaises(Overload) as ctx:
            propagate(tasks, 1)
        ev = ctx.exception.evidence
        self.assertEqual((ev.window_start, ev.window_end), (1, 2))
        self.assertEqual(set(ev.tasks), {"x", "y"})
        self.assertEqual(ev.required, 2)
        self.assertEqual(ev.capacity, 1)
        self.assertGreater(ev.required, ev.capacity)

    def test_domain_wipeout_returns_none(self):
        tasks = [
            Task("a", domain(0, 2), duration=2, demand=1),
            Task("b", frozenset({1}), duration=2, demand=1),
        ]
        self.assertIsNone(propagate(tasks, 1))

    def test_zero_duration_tasks_do_not_block_others(self):
        tasks = [
            Task("a", frozenset({0}), duration=2, demand=1),
            Task("z", domain(0, 1), duration=0, demand=9),
        ]
        pruned = propagate(tasks, 1)
        self.assertEqual(pruned[1], frozenset({0, 1}))


class CheckerTest(unittest.TestCase):
    def setUp(self):
        self.tasks = [
            Task("a", domain(0, 3), duration=2, demand=1),
            Task("b", domain(0, 3), duration=2, demand=1),
        ]

    def test_accepts_valid_solution(self):
        self.assertTrue(check_solution(self.tasks, 1, {"a": 0, "b": 2}))

    def test_rejects_capacity_violation(self):
        self.assertFalse(check_solution(self.tasks, 1, {"a": 0, "b": 1}))

    def test_rejects_start_outside_domain(self):
        self.assertFalse(check_solution(self.tasks, 1, {"a": 0, "b": 9}))

    def test_rejects_wrong_task_set(self):
        self.assertFalse(check_solution(self.tasks, 1, {"a": 0}))
        self.assertFalse(check_solution(self.tasks, 1, {"a": 0, "b": 2, "c": 0}))

    def test_zero_duration_occupies_nothing(self):
        tasks = [Task("z", domain(0, 1), duration=0, demand=100)]
        self.assertTrue(check_solution(tasks, 1, {"z": 0}))

    def test_back_to_back_tasks_do_not_overlap(self):
        self.assertTrue(check_solution(self.tasks, 1, {"a": 0, "b": 2}))
        self.assertTrue(check_solution(self.tasks, 1, {"a": 2, "b": 0}))


class SolverTest(unittest.TestCase):
    def test_solves_and_checker_verifies(self):
        tasks = [
            Task("alpha", domain(0, 2), duration=4, demand=2),
            Task("beta", domain(0, 6), duration=3, demand=2),
            Task("gamma", domain(0, 6), duration=2, demand=1),
        ]
        result = solve(tasks, 3)
        self.assertIsNotNone(result)
        self.assertTrue(check_solution(tasks, 3, result))

    def test_infeasible_returns_none(self):
        tasks = [
            Task("x", domain(0, 1), duration=2, demand=1),
            Task("y", domain(0, 1), duration=2, demand=1),
        ]
        self.assertIsNone(solve(tasks, 1))


class SoundnessPropertyTest(unittest.TestCase):
    """Randomized check: propagation must never remove a start value
    that appears in some legal schedule, never report feasible when the
    instance is infeasible, and the solver must agree with brute force."""

    def test_against_brute_force(self):
        rng = random.Random(20260929)
        for _ in range(400):
            capacity = rng.randint(1, 3)
            tasks = []
            for i in range(rng.randint(1, 4)):
                duration = rng.randint(0, 3)
                if duration > 0:
                    demand = rng.randint(0, capacity)
                else:
                    demand = rng.randint(0, capacity + 2)
                lo = rng.randint(0, 4)
                hi = lo + rng.randint(0, 3)
                tasks.append(Task(f"t{i}", domain(lo, hi), duration, demand))

            solutions = brute_force_solutions(tasks, capacity)

            try:
                pruned = propagate(tasks, capacity)
            except Overload:
                self.assertEqual(solutions, [],
                                 "overload raised on a feasible instance")
                continue
            if pruned is None:
                self.assertEqual(solutions, [],
                                 "domain wipe-out on a feasible instance")
                continue
            for sol in solutions:
                for task, kept in zip(tasks, pruned):
                    self.assertIn(
                        sol[task.name], kept,
                        f"legal value {sol[task.name]} of {task.name} pruned",
                    )

            result = solve(tasks, capacity)
            if solutions:
                self.assertIsNotNone(result, "solver missed existing solution")
                self.assertTrue(check_solution(tasks, capacity, result))
            else:
                self.assertIsNone(result, "solver claimed infeasible instance")


if __name__ == "__main__":
    unittest.main()
