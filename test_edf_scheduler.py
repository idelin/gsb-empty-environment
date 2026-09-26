"""Unit tests for edf_scheduler (Python 3.11, stdlib unittest)."""

import random
import unittest
from fractions import Fraction

from edf_scheduler import (
    OverloadWindow,
    Status,
    Task,
    find_overload_window,
    simulate,
)


def assert_schedule_valid(case, tasks, result):
    """Structural invariants every produced schedule must satisfy."""
    by_id = {t.task_id: t for t in tasks}
    # Segments are ordered, non-empty, non-overlapping.
    for prev, cur in zip(result.segments, result.segments[1:]):
        case.assertLess(prev.start, prev.end)
        case.assertLessEqual(prev.end, cur.start)
    # No execution before release; per-task total equals workload.
    executed = {t.task_id: Fraction(0) for t in tasks}
    for seg in result.segments:
        case.assertGreaterEqual(seg.start, by_id[seg.task_id].release)
        executed[seg.task_id] += seg.end - seg.start
    for tid, amount in executed.items():
        if tid in result.completions:
            case.assertEqual(amount, by_id[tid].work)


class TestFeasible(unittest.TestCase):
    def test_basic_preemption(self):
        tasks = [
            Task(1, 0, 4, 10),
            Task(2, 1, 2, 4),   # earlier deadline preempts task 1
            Task(3, 2, 1, 6),
        ]
        result = simulate(tasks)
        self.assertIs(result.status, Status.FEASIBLE)
        assert_schedule_valid(self, tasks, result)
        # Task 2 must preempt task 1 at time 1.
        self.assertEqual(result.segments[0].task_id, 1)
        self.assertEqual(result.segments[1].task_id, 2)
        self.assertEqual(result.segments[1].start, Fraction(1))
        for tid, done in result.completions.items():
            self.assertLessEqual(done, {t.task_id: t for t in tasks}[tid].deadline)

    def test_rational_workloads(self):
        tasks = [
            Task(1, 0, Fraction(3, 2), Fraction(5, 2)),
            Task(2, Fraction(1, 2), Fraction(3, 4), Fraction(7, 4)),
        ]
        result = simulate(tasks)
        self.assertIs(result.status, Status.FEASIBLE)
        assert_schedule_valid(self, tasks, result)
        self.assertEqual(result.completions[2], Fraction(5, 4))
        self.assertEqual(result.completions[1], Fraction(9, 4))

    def test_idle_jump_to_next_release(self):
        tasks = [Task(1, 10, 2, 20)]
        result = simulate(tasks)
        self.assertIs(result.status, Status.FEASIBLE)
        # Time jumps straight to the release; first segment starts at 10.
        self.assertEqual(result.segments[0].start, Fraction(10))
        self.assertEqual(result.completions[1], Fraction(12))

    def test_no_execution_before_release(self):
        tasks = [Task(1, 5, 3, 9), Task(2, 0, 1, 8)]
        result = simulate(tasks)
        self.assertIs(result.status, Status.FEASIBLE)
        for seg in result.segments:
            if seg.task_id == 1:
                self.assertGreaterEqual(seg.start, 5)


class TestOptimality(unittest.TestCase):
    """Preemptive EDF is optimal on a uniprocessor: feasibility under EDF
    matches the processor-demand criterion exactly."""

    def test_feasible_implies_optimal_flag(self):
        result = simulate([Task(1, 0, 1, 2)])
        self.assertIs(result.status, Status.FEASIBLE)
        self.assertTrue(result.optimal)

    def test_simulation_matches_demand_criterion(self):
        rng = random.Random(20260926)
        for _ in range(300):
            n = rng.randint(1, 7)
            tasks = []
            for i in range(n):
                release = rng.randint(0, 10)
                work = rng.randint(0, 6)
                deadline = release + rng.randint(0, 12)
                tasks.append(Task(i, release, work, deadline))
            result = simulate(tasks)
            window = find_overload_window(tasks)
            if window is None:
                self.assertIs(result.status, Status.FEASIBLE)
                assert_schedule_valid(self, tasks, result)
                for tid, done in result.completions.items():
                    self.assertLessEqual(done, tasks[tid].deadline)
            else:
                self.assertIs(result.status, Status.INFEASIBLE)
                self.assertIsNotNone(result.evidence)


class TestInfeasibleEvidence(unittest.TestCase):
    def test_overload_window_reported(self):
        tasks = [
            Task(1, 0, 2, 3),
            Task(2, 0, 2, 3),
            Task(3, 0, 1, 5),
        ]
        result = simulate(tasks)
        self.assertIs(result.status, Status.INFEASIBLE)
        self.assertEqual(result.missed, [2])
        ev = result.evidence
        self.assertIsNotNone(ev)
        self.assertEqual((ev.start, ev.end), (Fraction(0), Fraction(3)))
        self.assertEqual(ev.demand, Fraction(4))
        self.assertEqual(ev.capacity, Fraction(3))
        self.assertEqual(ev.task_ids, (1, 2))
        self.assertGreater(ev.excess, 0)

    def test_single_task_exceeds_own_window(self):
        result = simulate([Task(7, 2, 5, 4)])
        self.assertIs(result.status, Status.INFEASIBLE)
        self.assertEqual(result.missed, [7])
        ev = result.evidence
        self.assertEqual((ev.start, ev.end), (Fraction(2), Fraction(4)))
        self.assertEqual(ev.demand, Fraction(5))

    def test_largest_excess_window_chosen(self):
        tasks = [
            Task(1, 0, 3, 2),   # window [0,2]: demand 3, excess 1
            Task(2, 0, 3, 4),   # window [0,4]: demand 3+3=6, excess 2
        ]
        ev = find_overload_window(tasks)
        self.assertEqual((ev.start, ev.end), (Fraction(0), Fraction(4)))
        self.assertEqual(ev.excess, Fraction(2))

    def test_feasible_set_has_no_window(self):
        tasks = [Task(1, 0, 2, 4), Task(2, 1, 1, 3)]
        self.assertIsNone(find_overload_window(tasks))


class TestBudgetUndecided(unittest.TestCase):
    def test_budget_exhausted_is_undecided(self):
        tasks = [Task(i, 0, 1, 100 + i) for i in range(1, 6)]
        result = simulate(tasks, event_budget=2)
        self.assertIs(result.status, Status.UNDECIDED)
        self.assertIsNone(result.evidence)
        self.assertEqual(result.events_used, 3)  # stops right after exceeding
        self.assertLess(len(result.completions), len(tasks))

    def test_sufficient_budget_is_feasible(self):
        tasks = [Task(i, 0, 1, 100 + i) for i in range(1, 6)]
        result = simulate(tasks, event_budget=100)
        self.assertIs(result.status, Status.FEASIBLE)

    def test_zero_budget_on_nonempty_set(self):
        result = simulate([Task(1, 0, 1, 5)], event_budget=0)
        self.assertIs(result.status, Status.UNDECIDED)

    def test_negative_budget_rejected(self):
        with self.assertRaises(ValueError):
            simulate([Task(1, 0, 1, 5)], event_budget=-1)


class TestEdgeCases(unittest.TestCase):
    def test_zero_work_completes_immediately(self):
        tasks = [Task(1, 3, 0, 9), Task(2, 0, 2, 8)]
        result = simulate(tasks)
        self.assertIs(result.status, Status.FEASIBLE)
        self.assertEqual(result.completions[1], Fraction(3))
        # Zero-work task produces no execution segments.
        self.assertTrue(all(seg.task_id != 1 for seg in result.segments))

    def test_all_zero_work_no_segments(self):
        tasks = [Task(1, 0, 0, 1), Task(2, 5, 0, 6)]
        result = simulate(tasks)
        self.assertIs(result.status, Status.FEASIBLE)
        self.assertEqual(result.segments, [])
        self.assertEqual(result.completions, {1: Fraction(0), 2: Fraction(5)})

    def test_simultaneous_release_same_deadline_stable_by_id(self):
        tasks = [Task(3, 0, 1, 10), Task(1, 0, 1, 10), Task(2, 0, 1, 10)]
        result = simulate(tasks)
        self.assertIs(result.status, Status.FEASIBLE)
        order = [seg.task_id for seg in result.segments]
        self.assertEqual(order, [1, 2, 3])

    def test_same_deadline_earlier_release_first(self):
        tasks = [Task(1, 1, 1, 10), Task(2, 0, 1, 10)]
        result = simulate(tasks)
        order = [seg.task_id for seg in result.segments]
        self.assertEqual(order, [2, 1])

    def test_empty_task_set(self):
        result = simulate([])
        self.assertIs(result.status, Status.FEASIBLE)
        self.assertEqual(result.segments, [])

    def test_duplicate_ids_rejected(self):
        with self.assertRaises(ValueError):
            simulate([Task(1, 0, 1, 2), Task(1, 0, 1, 2)])

    def test_invalid_task_rejected(self):
        with self.assertRaises(ValueError):
            Task(1, 0, -1, 2)
        with self.assertRaises(ValueError):
            Task(1, 5, 1, 2)

    def test_event_count_is_jumps_not_ticks(self):
        # 100-unit job preempted once: 2 segments, not 100 ticks.
        tasks = [Task(1, 0, 100, 200), Task(2, 50, 1, 51)]
        result = simulate(tasks)
        self.assertIs(result.status, Status.FEASIBLE)
        self.assertEqual(len(result.segments), 3)
        self.assertEqual(result.events_used, 3)


if __name__ == "__main__":
    unittest.main(verbosity=2)
