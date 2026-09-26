import unittest
from fractions import Fraction

from edf_scheduler import (
    Segment,
    Status,
    Task,
    simulate,
    verify_evidence,
)


def assert_schedule_valid(test_case, tasks, result):
    """Invariants every produced schedule must satisfy."""
    by_id = {t.task_id: t for t in tasks}
    segments = result.segments
    for prev, cur in zip(segments, segments[1:]):
        test_case.assertLess(prev.start, prev.end)
        test_case.assertEqual(prev.end, cur.start)  # contiguous timeline
    for seg in segments:
        if seg.task_id is None:
            continue
        # never run before release
        test_case.assertGreaterEqual(seg.start, by_id[seg.task_id].release)
    if result.status is Status.OPTIMAL:
        # every task completed, none later than its deadline
        test_case.assertEqual(set(result.completions), set(by_id))
        for tid, finished in result.completions.items():
            test_case.assertLessEqual(finished, by_id[tid].deadline)
        # executed work per task equals its requirement
        done = {tid: Fraction(0) for tid in by_id}
        for seg in segments:
            if seg.task_id is not None:
                done[seg.task_id] += seg.end - seg.start
        for tid, amount in done.items():
            test_case.assertEqual(amount, by_id[tid].work)


class TestFeasible(unittest.TestCase):
    def test_preemption_by_earlier_deadline(self):
        tasks = [Task(1, 0, 2, 5), Task(2, 1, 1, 3)]
        result = simulate(tasks)
        self.assertIs(result.status, Status.OPTIMAL)
        self.assertTrue(result.feasible)
        self.assertTrue(result.optimal)
        self.assertEqual(
            result.segments,
            [
                Segment(0, 1, 1),
                Segment(1, 2, 2),  # task 2 preempts task 1
                Segment(2, 3, 1),
            ],
        )
        self.assertEqual(result.completions, {1: Fraction(3), 2: Fraction(2)})
        # event jumping: 2 releases + 2 completions, no tick loop
        self.assertEqual(result.events_processed, 4)
        assert_schedule_valid(self, tasks, result)

    def test_simultaneous_release_same_deadline_stable_by_id(self):
        tasks = [Task(3, 0, 1, 10), Task(1, 0, 1, 10), Task(2, 0, 1, 10)]
        result = simulate(tasks)
        self.assertIs(result.status, Status.OPTIMAL)
        order = [seg.task_id for seg in result.segments]
        self.assertEqual(order, [1, 2, 3])
        assert_schedule_valid(self, tasks, result)

    def test_zero_work_completes_immediately(self):
        tasks = [Task(1, 2, 0, 2), Task(2, 0, 2, 5)]
        result = simulate(tasks)
        self.assertIs(result.status, Status.OPTIMAL)
        self.assertEqual(result.completions[1], Fraction(2))  # at its release
        # zero-work task never occupies the processor
        self.assertEqual(result.segments, [Segment(0, 2, 2)])
        assert_schedule_valid(self, tasks, result)

    def test_no_execution_before_release(self):
        tasks = [Task(1, 5, 1, 10)]
        result = simulate(tasks)
        self.assertIs(result.status, Status.OPTIMAL)
        self.assertEqual(result.segments, [Segment(5, 6, 1)])
        assert_schedule_valid(self, tasks, result)

    def test_idle_gap_recorded(self):
        tasks = [Task(1, 0, 1, 2), Task(2, 5, 1, 6)]
        result = simulate(tasks)
        self.assertIs(result.status, Status.OPTIMAL)
        self.assertEqual(
            result.segments,
            [Segment(0, 1, 1), Segment(1, 5, None), Segment(5, 6, 2)],
        )
        assert_schedule_valid(self, tasks, result)

    def test_rational_numbers_exact(self):
        tasks = [
            Task(1, 0, Fraction(1, 3), Fraction(1, 2)),
            Task(2, Fraction(1, 4), Fraction(1, 3), Fraction(3, 4)),
        ]
        result = simulate(tasks)
        self.assertIs(result.status, Status.OPTIMAL)
        self.assertEqual(result.completions[1], Fraction(1, 3))
        self.assertEqual(result.completions[2], Fraction(2, 3))
        assert_schedule_valid(self, tasks, result)

    def test_event_jumping_with_huge_numbers(self):
        big = 10**12
        tasks = [Task(1, 0, big, big + 2), Task(2, big // 10, 1, big // 10 + 1)]
        result = simulate(tasks)
        self.assertIs(result.status, Status.OPTIMAL)
        # 2 releases + 2 completions regardless of the magnitude of the work
        self.assertEqual(result.events_processed, 4)
        self.assertEqual(result.completions[1], Fraction(big + 1))
        assert_schedule_valid(self, tasks, result)

    def test_empty_instance_is_optimal(self):
        result = simulate([])
        self.assertIs(result.status, Status.OPTIMAL)
        self.assertEqual(result.segments, [])
        self.assertEqual(result.completions, {})


class TestInfeasible(unittest.TestCase):
    def test_overloaded_window_gives_evidence(self):
        tasks = [Task(1, 0, 3, 4), Task(2, 0, 3, 4)]
        result = simulate(tasks)
        self.assertIs(result.status, Status.INFEASIBLE)
        self.assertFalse(result.feasible)
        ev = result.evidence
        self.assertIsNotNone(ev)
        self.assertEqual(ev.missed_task_id, 2)
        self.assertEqual((ev.window_start, ev.window_end), (Fraction(0), Fraction(4)))
        self.assertEqual(ev.demand, Fraction(6))
        self.assertEqual(ev.supply, Fraction(4))
        self.assertGreater(ev.demand, ev.supply)
        self.assertTrue(verify_evidence(tasks, ev))

    def test_evidence_window_excludes_late_deadline_work(self):
        tasks = [
            Task(1, 0, 2, 10),  # long-deadline filler, runs first
            Task(2, 1, 3, 4),
            Task(3, 1, 3, 4),  # tasks 2+3 need 6 units inside [1, 4]
        ]
        result = simulate(tasks)
        self.assertIs(result.status, Status.INFEASIBLE)
        ev = result.evidence
        self.assertEqual(ev.missed_task_id, 3)
        self.assertEqual((ev.window_start, ev.window_end), (Fraction(1), Fraction(4)))
        self.assertEqual(ev.demand, Fraction(6))
        self.assertEqual(ev.supply, Fraction(3))
        self.assertTrue(verify_evidence(tasks, ev))

    def test_zero_length_window_when_work_equals_deadline(self):
        # positive work with deadline == release can never be scheduled
        tasks = [Task(1, 3, 1, 3)]
        result = simulate(tasks)
        self.assertIs(result.status, Status.INFEASIBLE)
        ev = result.evidence
        self.assertEqual((ev.window_start, ev.window_end), (Fraction(3), Fraction(3)))
        self.assertEqual(ev.supply, Fraction(0))
        self.assertTrue(verify_evidence(tasks, ev))

    def test_rational_overload(self):
        tasks = [
            Task(1, 0, Fraction(2, 3), Fraction(1, 2)),
            Task(2, 0, Fraction(2, 3), Fraction(1, 2)),
        ]
        result = simulate(tasks)
        self.assertIs(result.status, Status.INFEASIBLE)
        ev = result.evidence
        self.assertEqual(ev.demand, Fraction(4, 3))
        self.assertEqual(ev.supply, Fraction(1, 2))
        self.assertTrue(verify_evidence(tasks, ev))


class TestBudget(unittest.TestCase):
    def test_budget_exhaustion_is_undecided(self):
        tasks = [Task(i, 0, 1, 10) for i in range(5)]
        result = simulate(tasks, max_events=2)
        self.assertIs(result.status, Status.UNDECIDED)
        self.assertFalse(result.feasible)
        self.assertIsNone(result.evidence)

    def test_same_instance_decided_with_enough_budget(self):
        tasks = [Task(i, 0, 1, 10) for i in range(5)]
        result = simulate(tasks, max_events=100)
        self.assertIs(result.status, Status.OPTIMAL)
        assert_schedule_valid(self, tasks, result)


class TestValidation(unittest.TestCase):
    def test_negative_work_rejected(self):
        with self.assertRaises(ValueError):
            Task(1, 0, -1, 5)

    def test_deadline_before_release_rejected(self):
        with self.assertRaises(ValueError):
            Task(1, 5, 1, 4)

    def test_duplicate_ids_rejected(self):
        with self.assertRaises(ValueError):
            simulate([Task(1, 0, 1, 2), Task(1, 0, 1, 2)])

    def test_tuple_input_accepted(self):
        result = simulate([(1, 0, 1, 2)])
        self.assertIs(result.status, Status.OPTIMAL)
        self.assertEqual(result.completions, {1: Fraction(1)})


if __name__ == "__main__":
    unittest.main()
