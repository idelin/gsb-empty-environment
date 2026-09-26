"""Unit tests for scheduler.py (standard-library unittest)."""

import random
import unittest
from fractions import Fraction
from itertools import permutations

from scheduler import (
    Job,
    Status,
    build_schedule,
    constant_setup,
    lower_bound,
    matrix_setup,
    solve,
    zero_setup,
)


def brute_force_optimum(jobs, setup):
    """Exact optimum by full permutation enumeration (ground truth)."""
    best = None
    for perm in permutations(range(len(jobs))):
        total = sum(entry.tardiness for entry in build_schedule(jobs, perm, setup))
        if best is None or total < best:
            best = total
    return best if best is not None else 0


def validate_schedule(jobs, schedule, setup, reported_total):
    """A schedule must use each job once and respect releases and setups."""
    assert schedule is not None
    assert sorted(entry.job.job_id for entry in schedule) == sorted(
        job.job_id for job in jobs
    )
    time_now = 0
    last_type = None
    total = 0
    for entry in schedule:
        job = entry.job
        assert entry.start >= job.release
        assert entry.start >= time_now + setup(last_type, job.job_type)
        assert entry.completion == entry.start + job.processing
        assert entry.tardiness == max(0, entry.completion - job.deadline)
        total += entry.tardiness
        time_now = entry.completion
        last_type = job.job_type
    assert total == reported_total


def random_instance(rng, n, types, max_release=8, max_proc=5, max_due=20):
    return [
        Job(
            f"J{i}",
            rng.randint(0, max_release),
            rng.randint(1, max_proc),
            rng.randint(1, max_due),
            rng.choice(types),
        )
        for i in range(n)
    ]


class TestBasics(unittest.TestCase):
    def test_empty_instance_is_optimal(self):
        result = solve([], zero_setup())
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.total_tardiness, 0)
        self.assertEqual(result.schedule, ())

    def test_invalid_job_rejected(self):
        with self.assertRaises(ValueError):
            Job("bad", 0, 0, 1, "A")  # zero processing
        with self.assertRaises(ValueError):
            Job("bad", -1, 1, 1, "A")  # negative release

    def test_same_type_needs_no_setup(self):
        jobs = [Job("a", 0, 2, 5, "T"), Job("b", 0, 2, 9, "T")]
        result = solve(jobs, constant_setup(7))
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual([e.completion for e in result.schedule], [2, 4])

    def test_setup_charged_across_types(self):
        jobs = [Job("a", 0, 2, 100, "A"), Job("b", 0, 1, 100, "B")]
        result = solve(jobs, constant_setup(3))
        self.assertEqual(result.status, Status.OPTIMAL)
        first, second = result.schedule
        self.assertEqual(second.start, first.completion + 3)

    def test_idle_beats_immediate_execution(self):
        # Jb is available at t=0, but idling until Ja's release is optimal.
        jobs = [Job("Ja", 1, 1, 2, "X"), Job("Jb", 0, 10, 1000, "X")]
        result = solve(jobs, constant_setup(0))
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.total_tardiness, 0)
        self.assertEqual(result.schedule[0].job.job_id, "Ja")
        self.assertEqual(result.schedule[0].start, 1)  # machine idles [0, 1)


class TestOptimalityAgainstBruteForce(unittest.TestCase):
    def test_integer_instances_match_brute_force(self):
        rng = random.Random(20260926)
        for trial in range(10):
            jobs = random_instance(rng, n=6, types=("A", "B"))
            setup = constant_setup(rng.randint(0, 3))
            result = solve(jobs, setup)
            self.assertEqual(result.status, Status.OPTIMAL)
            self.assertEqual(
                result.total_tardiness,
                brute_force_optimum(jobs, setup),
                msg=f"trial {trial}",
            )
            validate_schedule(jobs, result.schedule, setup, result.total_tardiness)
            self.assertEqual(result.certified_lower_bound, result.total_tardiness)

    def test_rational_instances_match_brute_force(self):
        rng = random.Random(7)
        halves = lambda lo, hi: Fraction(rng.randint(lo, 2 * hi), 2)
        for trial in range(8):
            jobs = [
                Job(
                    f"R{i}",
                    max(Fraction(0), halves(0, 6)),
                    halves(1, 4),
                    halves(1, 12),
                    rng.choice(("alpha", "beta")),
                )
                for i in range(5)
            ]
            setup = matrix_setup(
                {
                    "alpha": {"beta": Fraction(1, 2)},
                    "beta": {"alpha": Fraction(3, 4)},
                }
            )
            result = solve(jobs, setup)
            self.assertEqual(result.status, Status.OPTIMAL)
            self.assertEqual(
                result.total_tardiness, brute_force_optimum(jobs, setup)
            )
            self.assertIsInstance(result.total_tardiness, (int, Fraction))
            validate_schedule(jobs, result.schedule, setup, result.total_tardiness)


class TestLowerBound(unittest.TestCase):
    def test_lower_bound_never_exceeds_optimum(self):
        rng = random.Random(99)
        for _ in range(20):
            jobs = random_instance(rng, n=6, types=("A", "B", "C"))
            bound = lower_bound(0, jobs)
            optimum = brute_force_optimum(jobs, constant_setup(2))
            self.assertLessEqual(bound, optimum)

    def test_lower_bound_exact_without_setups(self):
        jobs = [Job("a", 0, 2, 1, "A"), Job("b", 0, 3, 2, "A")]
        # No setups, both released: a finishes at 7 (tardy 6), b at 10
        # (tardy 8), so the bound of 14 is tight here.
        self.assertEqual(lower_bound(5, jobs), 14)
        self.assertEqual(lower_bound(5, []), 0)
        self.assertEqual(lower_bound(0, []), 0)


class TestBudgets(unittest.TestCase):
    def setUp(self):
        rng = random.Random(4242)
        self.jobs = random_instance(rng, n=7, types=("A", "B"))
        self.setup = constant_setup(2)
        self.full = solve(self.jobs, self.setup)
        assert self.full.status is Status.OPTIMAL

    def test_zero_node_budget_is_undecided(self):
        result = solve(self.jobs, self.setup, node_budget=0)
        self.assertEqual(result.status, Status.UNDECIDED)
        self.assertIsNone(result.schedule)
        self.assertIsNone(result.total_tardiness)
        self.assertTrue(result.budget_exhausted)
        self.assertLessEqual(result.certified_lower_bound, self.full.total_tardiness)

    def test_partial_budget_returns_feasible_not_optimal(self):
        budget = self.full.nodes_explored - 1
        result = solve(self.jobs, self.setup, node_budget=budget)
        self.assertEqual(result.status, Status.FEASIBLE)
        self.assertNotEqual(result.status, Status.OPTIMAL)
        self.assertTrue(result.budget_exhausted)
        # The incumbent is a genuine schedule, at least as good as claimed.
        validate_schedule(
            self.jobs, result.schedule, self.setup, result.total_tardiness
        )
        self.assertGreaterEqual(result.total_tardiness, self.full.total_tardiness)
        self.assertLessEqual(
            result.certified_lower_bound, self.full.total_tardiness
        )

    def test_zero_time_budget_stops_immediately(self):
        result = solve(self.jobs, self.setup, time_budget=0.0)
        self.assertEqual(result.status, Status.UNDECIDED)
        self.assertTrue(result.budget_exhausted)

    def test_generous_budget_proves_optimality(self):
        result = solve(
            self.jobs,
            self.setup,
            node_budget=10**9,
            time_budget=60.0,
        )
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.total_tardiness, self.full.total_tardiness)

    def test_negative_budget_rejected(self):
        with self.assertRaises(ValueError):
            solve(self.jobs, self.setup, node_budget=-1)
        with self.assertRaises(ValueError):
            solve(self.jobs, self.setup, time_budget=-0.5)


if __name__ == "__main__":
    unittest.main()
