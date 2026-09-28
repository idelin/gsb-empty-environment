"""Unit tests for the cumulative scheduling module (stdlib unittest)."""

import itertools
import random
import unittest

from cumulative import (
    DomainWipeoutError,
    InfeasibleError,
    OverloadError,
    Task,
    check,
    propagate,
    solve,
)


def brute_force_solutions(tasks, capacity):
    """Enumerate every feasible assignment by exhaustive search."""
    solutions = []
    names = [t.name for t in tasks]
    domains = [sorted(t.domain) for t in tasks]
    for combo in itertools.product(*domains):
        assignment = dict(zip(names, combo))
        usage = {}
        ok = True
        for task in tasks:
            for time in range(assignment[task.name], assignment[task.name] + task.duration):
                usage[time] = usage.get(time, 0) + task.demand
                if usage[time] > capacity:
                    ok = False
                    break
            if not ok:
                break
        if ok:
            solutions.append(assignment)
    return solutions


def random_instance(rng):
    capacity = rng.randint(1, 4)
    tasks = []
    for i in range(rng.randint(1, 5)):
        duration = rng.randint(0, 4)
        demand = rng.randint(0, 3)
        domain = {s for s in range(8) if rng.random() < 0.6} or {0}
        tasks.append(Task(f"t{i}", duration, demand, domain))
    return tasks, capacity


class ValidationTests(unittest.TestCase):
    def test_single_task_over_capacity_fails_immediately(self):
        tasks = [Task("big", 3, 5, {0, 1, 2})]
        with self.assertRaises(OverloadError) as ctx:
            propagate(tasks, 4)
        evidence = ctx.exception.evidence[0]
        self.assertEqual(evidence.contributors, ("big",))
        self.assertEqual(evidence.demand, 5)
        self.assertEqual(evidence.capacity, 4)
        self.assertIsNone(solve(tasks, 4))

    def test_zero_duration_task_occupies_nothing(self):
        # Demand far above capacity, but duration 0 -> no occupation, no failure.
        tasks = [
            Task("ghost", 0, 100, {0, 1, 2}),
            Task("real", 2, 1, {0, 1, 2}),
        ]
        domains = propagate(tasks, 1)
        self.assertEqual(domains["ghost"], {0, 1, 2})
        assignment = solve(tasks, 1)
        self.assertIsNotNone(assignment)
        self.assertTrue(check(tasks, 1, assignment))

    def test_invalid_input_rejected(self):
        with self.assertRaises(ValueError):
            propagate([Task("a", -1, 1, {0})], 1)
        with self.assertRaises(ValueError):
            propagate([Task("a", 1, -1, {0})], 1)
        with self.assertRaises(ValueError):
            propagate([Task("a", 1, 1, set())], 1)
        with self.assertRaises(ValueError):
            propagate([Task("a", 1, 1, {0}), Task("a", 1, 1, {0})], 1)
        with self.assertRaises(ValueError):
            propagate([Task("a", 1, 1, {0})], -1)


class PropagationTests(unittest.TestCase):
    def test_compulsory_overload_gives_window_evidence(self):
        # Both tasks must cover [1, 4): lst=1, ect=0+4=4.
        tasks = [
            Task("x", 4, 1, {0, 1}),
            Task("y", 4, 1, {0, 1}),
        ]
        with self.assertRaises(OverloadError) as ctx:
            propagate(tasks, 1)
        evidence = ctx.exception.evidence
        self.assertEqual(len(evidence), 1)
        self.assertEqual(evidence[0].window, (1, 4))
        self.assertEqual(evidence[0].demand, 2)
        self.assertEqual(evidence[0].capacity, 1)
        self.assertEqual(set(evidence[0].contributors), {"x", "y"})

    def test_timetable_filtering_prunes_impossible_starts(self):
        # A has compulsory part [3, 5) using 1 of 2 capacity units, so B
        # (demand 2, duration 2) cannot cover time 3 or 4.
        tasks = [
            Task("a", 5, 1, {0, 1, 2, 3}),
            Task("b", 2, 2, {0, 1, 2, 3, 4, 5, 6}),
        ]
        domains = propagate(tasks, 2)
        self.assertEqual(domains["b"], {0, 1, 5, 6})
        self.assertEqual(domains["a"], {0, 1, 2, 3})

    def test_domain_wipeout_reports_blocking_times(self):
        # A's compulsory part is [1, 3); B has no compulsory part itself,
        # but every start in {0, 1, 2} would overlap [1, 3).
        tasks = [
            Task("a", 3, 1, {0, 1}),
            Task("b", 2, 1, {0, 1, 2}),
        ]
        with self.assertRaises(DomainWipeoutError) as ctx:
            propagate(tasks, 1)
        self.assertEqual(ctx.exception.task_name, "b")
        self.assertEqual(set(ctx.exception.reasons), {0, 1, 2})

    def test_propagation_never_removes_legal_values(self):
        rng = random.Random(20260929)
        for _ in range(300):
            tasks, capacity = random_instance(rng)
            try:
                domains = propagate(tasks, capacity)
            except InfeasibleError:
                # Proven infeasible: brute force must agree.
                self.assertEqual(brute_force_solutions(tasks, capacity), [])
                continue
            for solution in brute_force_solutions(tasks, capacity):
                for name, start in solution.items():
                    self.assertIn(start, domains[name])


class SolverTests(unittest.TestCase):
    def test_solver_finds_valid_schedule(self):
        tasks = [
            Task("a", 3, 1, set(range(6))),
            Task("b", 2, 1, set(range(6))),
            Task("c", 4, 2, set(range(6))),
        ]
        assignment = solve(tasks, 2)
        self.assertIsNotNone(assignment)
        self.assertTrue(check(tasks, 2, assignment))

    def test_solver_returns_none_when_unsat(self):
        tasks = [
            Task("a", 2, 1, {0}),
            Task("b", 2, 1, {0}),
        ]
        self.assertIsNone(solve(tasks, 1))

    def test_solver_agrees_with_brute_force(self):
        rng = random.Random(112233)
        for _ in range(300):
            tasks, capacity = random_instance(rng)
            assignment = solve(tasks, capacity)
            solutions = brute_force_solutions(tasks, capacity)
            if solutions:
                self.assertIsNotNone(assignment)
                self.assertTrue(check(tasks, capacity, assignment))
            else:
                self.assertIsNone(assignment)


class CheckerTests(unittest.TestCase):
    def setUp(self):
        self.tasks = [
            Task("a", 2, 1, {0, 1, 2}),
            Task("b", 2, 1, {0, 1, 2}),
        ]

    def test_accepts_valid_schedule(self):
        self.assertTrue(check(self.tasks, 2, {"a": 0, "b": 0}))
        self.assertTrue(check(self.tasks, 1, {"a": 0, "b": 2}))

    def test_rejects_capacity_violation(self):
        self.assertFalse(check(self.tasks, 1, {"a": 0, "b": 0}))
        self.assertFalse(check(self.tasks, 1, {"a": 0, "b": 1}))

    def test_rejects_start_outside_original_domain(self):
        self.assertFalse(check(self.tasks, 2, {"a": 0, "b": 5}))

    def test_rejects_missing_or_extra_tasks(self):
        self.assertFalse(check(self.tasks, 2, {"a": 0}))
        self.assertFalse(check(self.tasks, 2, {"a": 0, "b": 1, "c": 0}))


if __name__ == "__main__":
    unittest.main(verbosity=2)
