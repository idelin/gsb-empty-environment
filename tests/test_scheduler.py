import itertools
import random
import unittest
from fractions import Fraction

from scheduler import (
    BranchAndBoundSolver,
    Job,
    Schedule,
    SearchBudget,
    SetupMatrix,
    Status,
    build_schedule,
)


def brute_force_optimum(jobs, setups):
    """穷举全部排列求最优总迟延，用于对照。"""
    best = None
    for perm in itertools.permutations(range(len(jobs))):
        obj = build_schedule([jobs[i] for i in perm], setups).total_tardiness
        if best is None or obj < best:
            best = obj
    return best if best is not None else Fraction(0)


def random_instance(rng, n, rational=False):
    types = ["X", "Y", "Z"]
    jobs = []
    for i in range(n):
        p = rng.randint(1, 6)
        r = rng.randint(0, 8)
        d = rng.randint(1, 20)
        if rational:
            p = Fraction(p, 2)
            r = Fraction(r, 2)
            d = Fraction(d, 2)
        jobs.append(Job(f"J{i}", rng.choice(types), release=r, processing=p, due=d))
    pairs = {}
    for a in types:
        for b in types:
            if a < b:
                pairs[(a, b)] = rng.randint(0, 3)
    return jobs, SetupMatrix.symmetric(pairs)


class TestModel(unittest.TestCase):
    def test_same_type_no_setup(self):
        setups = SetupMatrix.uniform(["X", "Y"], 5)
        jobs = [Job("A", "X", 0, 2, 10), Job("B", "X", 0, 3, 10)]
        sched = build_schedule(jobs, setups)
        self.assertEqual(sched.entries[1].setup, 0)
        self.assertEqual(sched.entries[1].start, 2)

    def test_different_type_setup_charged(self):
        setups = SetupMatrix.symmetric({("X", "Y"): 4})
        jobs = [Job("A", "X", 0, 2, 10), Job("B", "Y", 0, 3, 10)]
        sched = build_schedule(jobs, setups)
        self.assertEqual(sched.entries[1].setup, 4)
        self.assertEqual(sched.entries[1].start, 6)

    def test_forced_idle_before_release(self):
        jobs = [Job("A", "X", 10, 2, 20)]
        sched = build_schedule(jobs, SetupMatrix())
        self.assertEqual(sched.entries[0].start, 10)
        self.assertEqual(sched.entries[0].idle, 10)

    def test_rational_data_exact(self):
        jobs = [
            Job("A", "X", release="1/2", processing=Fraction(3, 2), due=2),
            Job("B", "X", release=0, processing="0.5", due=Fraction(5, 2)),
        ]
        sched = build_schedule(jobs, SetupMatrix())
        self.assertEqual(sched.entries[0].completion, Fraction(2))
        self.assertEqual(sched.entries[1].completion, Fraction(5, 2))
        self.assertEqual(sched.total_tardiness, Fraction(0))
        self.assertIsInstance(sched.total_tardiness, Fraction)

    def test_setup_matrix_forms(self):
        u = SetupMatrix.uniform(["X", "Y"], 3)
        s = SetupMatrix.symmetric({("X", "Y"): 3})
        self.assertEqual(u.between("X", "Y"), 3)
        self.assertEqual(s.between("Y", "X"), 3)
        self.assertEqual(u.between("X", "X"), 0)
        d = SetupMatrix(default=2)
        self.assertEqual(d.between("P", "Q"), 2)
        self.assertEqual(d.between("P", "P"), 0)


class TestSolver(unittest.TestCase):
    def test_idle_can_beat_immediate_execution(self):
        setups = SetupMatrix.symmetric({("X", "Y"): 1})
        jobs = [
            Job("A", "X", release=0, processing=10, due=10),
            Job("B", "Y", release=3, processing=2, due=5),
        ]
        result = BranchAndBoundSolver(jobs, setups).solve()
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.objective, 6)
        self.assertEqual(result.schedule.order, ("B", "A"))
        self.assertEqual(result.schedule.entries[0].idle, 3)  # 主动空等 B 释放

    def test_matches_brute_force_integer(self):
        rng = random.Random(20260926)
        for _ in range(8):
            jobs, setups = random_instance(rng, n=6)
            result = BranchAndBoundSolver(jobs, setups).solve()
            self.assertEqual(result.status, Status.OPTIMAL)
            self.assertEqual(result.objective, brute_force_optimum(jobs, setups))
            self.assertEqual(result.lower_bound, result.objective)
            self.assertEqual(result.schedule.violations(setups), [])

    def test_matches_brute_force_rational(self):
        rng = random.Random(7)
        for _ in range(5):
            jobs, setups = random_instance(rng, n=6, rational=True)
            result = BranchAndBoundSolver(jobs, setups).solve()
            self.assertEqual(result.status, Status.OPTIMAL)
            self.assertEqual(result.objective, brute_force_optimum(jobs, setups))

    def test_budget_returns_feasible_not_optimal(self):
        rng = random.Random(99)
        jobs, setups = random_instance(rng, n=7)
        optimum = brute_force_optimum(jobs, setups)

        limited = BranchAndBoundSolver(jobs, setups, SearchBudget(max_nodes=1)).solve()
        self.assertEqual(limited.status, Status.FEASIBLE)   # 预算未决，不贴最优标签
        self.assertFalse(limited.proven_optimal)
        self.assertTrue(limited.budget_exhausted)
        self.assertLessEqual(limited.nodes_explored, 1)
        self.assertIsNotNone(limited.schedule)              # 仍返回最佳可行解
        self.assertEqual(limited.schedule.violations(setups), [])
        self.assertGreaterEqual(limited.objective, optimum)  # 上界不优于真最优
        self.assertLessEqual(limited.lower_bound, optimum)   # 下界有效

        full = BranchAndBoundSolver(jobs, setups).solve()
        self.assertEqual(full.status, Status.OPTIMAL)
        self.assertEqual(full.objective, optimum)

    def test_time_budget(self):
        rng = random.Random(5)
        jobs, setups = random_instance(rng, n=7)
        result = BranchAndBoundSolver(jobs, setups, SearchBudget(max_seconds=0)).solve()
        self.assertEqual(result.status, Status.FEASIBLE)
        self.assertIsNotNone(result.schedule)

    def test_lower_bound_never_exceeds_optimum(self):
        rng = random.Random(1234)
        for _ in range(6):
            jobs, setups = random_instance(rng, n=7)
            optimum = brute_force_optimum(jobs, setups)
            for budget in (SearchBudget(max_nodes=3), SearchBudget(max_nodes=20)):
                result = BranchAndBoundSolver(jobs, setups, budget).solve()
                self.assertLessEqual(result.lower_bound, optimum)
                self.assertGreaterEqual(result.objective, optimum)

    def test_empty_and_single_job(self):
        empty = BranchAndBoundSolver([], SetupMatrix()).solve()
        self.assertEqual(empty.status, Status.OPTIMAL)
        self.assertEqual(empty.objective, 0)

        one = BranchAndBoundSolver([Job("A", "X", 2, 3, 4)], SetupMatrix()).solve()
        self.assertEqual(one.status, Status.OPTIMAL)
        self.assertEqual(one.objective, 1)  # 完工 5，截止 4
        self.assertEqual(one.schedule.order, ("A",))

    def test_duplicate_job_id_rejected(self):
        jobs = [Job("A", "X", 0, 1, 1), Job("A", "Y", 0, 1, 1)]
        with self.assertRaises(ValueError):
            BranchAndBoundSolver(jobs, SetupMatrix())


if __name__ == "__main__":
    unittest.main()
