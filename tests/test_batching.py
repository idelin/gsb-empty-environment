"""batching 模块的单元测试（仅标准库 unittest）。"""

import random
import unittest
from fractions import Fraction

from batching import Instance, Job, Status, solve, verify


def brute_force_min_batches(instance):
    """独立暴力解：枚举所有集合划分，按批内最早截止 EDF 排序检查可行性。

    返回最少批数；不可行返回 None。仅用于小规模对拍。
    """
    jobs = instance.jobs
    n = len(jobs)
    best = [None]
    assign = [0] * n

    def feasible(batches):
        groups = [[] for _ in range(batches)]
        for idx, g in enumerate(assign):
            groups[g].append(idx)
        timed = []
        for g in groups:
            members = [jobs[i] for i in g]
            if len({j.job_type for j in members}) > 1:
                return False
            if sum((j.resource for j in members), Fraction(0)) > instance.capacity:
                return False
            timed.append((min(j.deadline for j in members),
                          sum((j.proc_time for j in members), Fraction(0))))
        timed.sort()
        clock = Fraction(0)
        for dl, proc in timed:
            clock += proc
            if clock > dl:
                return False
        return True

    def rec(i, used):
        if best[0] is not None and used >= best[0]:
            return
        if i == n:
            if feasible(used):
                best[0] = used
            return
        for g in range(used + 1):
            assign[i] = g
            rec(i + 1, max(used, g + 1))

    rec(0, 0)
    return best[0]


class TestHardConstraints(unittest.TestCase):
    def test_incompatible_jobs_never_mixed(self):
        # 两个不相容作业资源合计 10 <= 容量 10：同批可省一半成本，但必须分开
        inst = Instance(
            [Job("a", "gpu", 5, 1, 100), Job("b", "io", 5, 1, 100)],
            capacity=10, startup_cost=40,
        )
        sol = solve(inst)
        self.assertEqual(sol.status, Status.OPTIMAL)
        self.assertEqual(sol.num_batches, 2)
        self.assertEqual(sol.total_cost, Fraction(80))
        for b in sol.batches:
            self.assertEqual(len({inst.jobs[i].job_type for i in b}), 1)

    def test_startup_cost_charged_per_batch_not_per_job(self):
        # 3 个相容作业同批：成本 = 1 次启动成本，而非 3 次
        inst = Instance(
            [Job(f"j{k}", "gpu", 3, 1, 100) for k in range(3)],
            capacity=10, startup_cost=30,
        )
        sol = solve(inst)
        self.assertEqual(sol.status, Status.OPTIMAL)
        self.assertEqual(sol.num_batches, 1)
        self.assertEqual(sol.total_cost, Fraction(30))

    def test_oversize_single_job_rejected(self):
        inst = Instance(
            [Job("big", "gpu", 11, 1, 10), Job("ok", "gpu", 4, 1, 10)],
            capacity=10, startup_cost=10,
        )
        sol = solve(inst)
        self.assertEqual(sol.status, Status.INFEASIBLE)
        self.assertIn("big", sol.message)

    def test_job_is_indivisible(self):
        # 每个作业资源 6 > 容量 10 的一半：若可拆分 2 个作业只需 2 批装 12/10？
        # 不可拆 => 每作业独占一批，共 2 批；且任何批资源不超容量
        inst = Instance(
            [Job("a", "gpu", 6, 1, 100), Job("b", "gpu", 6, 1, 100)],
            capacity=10, startup_cost=10,
        )
        sol = solve(inst)
        self.assertEqual(sol.status, Status.OPTIMAL)
        self.assertEqual(sol.num_batches, 2)
        ok, errors = verify(inst, sol.batches)
        self.assertTrue(ok, errors)

    def test_deadline_infeasible_proven(self):
        inst = Instance(
            [Job("a", "gpu", 6, 4, 3), Job("b", "gpu", 5, 4, 100)],
            capacity=10, startup_cost=10,
        )
        sol = solve(inst)
        self.assertEqual(sol.status, Status.INFEASIBLE)
        self.assertIsNone(sol.total_cost)

    def test_empty_instance(self):
        sol = solve(Instance([], capacity=10, startup_cost=10))
        self.assertEqual(sol.status, Status.OPTIMAL)
        self.assertEqual(sol.total_cost, Fraction(0))


class TestExactValues(unittest.TestCase):
    def test_rational_data_exact(self):
        inst = Instance(
            [
                Job("a", "gpu", Fraction(10, 3), Fraction(1, 2), Fraction(5)),
                Job("b", "gpu", Fraction(10, 3), Fraction(1, 2), Fraction(5)),
                Job("c", "gpu", Fraction(10, 3), Fraction(1, 2), Fraction(5)),
            ],
            capacity=Fraction(20, 3), startup_cost=Fraction(7, 2),
        )
        sol = solve(inst)
        # 每批最多装 2 个（3 个合计 10 > 20/3）=> 2 批
        self.assertEqual(sol.status, Status.OPTIMAL)
        self.assertEqual(sol.num_batches, 2)
        self.assertEqual(sol.total_cost, Fraction(7))
        ok, errors = verify(inst, sol.batches)
        self.assertTrue(ok, errors)

    def test_deadline_forces_extra_batch(self):
        # 容量允许 1 批，但同批完成时刻 13 会错过截止 3 => 必须 2 批
        inst = Instance(
            [Job("x", "gpu", 6, 3, 3), Job("y", "gpu", 4, 10, 100)],
            capacity=10, startup_cost=25,
        )
        sol = solve(inst)
        self.assertEqual(sol.status, Status.OPTIMAL)
        self.assertEqual(sol.num_batches, 2)
        ok, errors = verify(inst, sol.batches)
        self.assertTrue(ok, errors)

    def test_solution_always_verifiable(self):
        rng = random.Random(2024)
        for _ in range(20):
            n = rng.randint(1, 9)
            jobs = [
                Job(
                    f"j{k}",
                    rng.choice(["gpu", "io"]),
                    rng.randint(1, 10),
                    rng.randint(1, 4),
                    rng.randint(4, 40),
                )
                for k in range(n)
            ]
            inst = Instance(jobs, capacity=10, startup_cost=rng.randint(1, 9))
            sol = solve(inst)
            if sol.status == Status.OPTIMAL:
                ok, errors = verify(inst, sol.batches)
                self.assertTrue(ok, errors)
                self.assertEqual(sol.total_cost, inst.startup_cost * sol.num_batches)


class TestBudgetSemantics(unittest.TestCase):
    def test_budget_exhausted_feasible_incumbent(self):
        inst = Instance(
            [Job(f"j{k}", "gpu", 6, 1, 100) for k in range(3)],
            capacity=10, startup_cost=25,
        )
        sol = solve(inst, transition_budget=5)
        self.assertEqual(sol.status, Status.FEASIBLE)
        self.assertIsNotNone(sol.total_cost)
        ok, errors = verify(inst, sol.batches)
        self.assertTrue(ok, errors)

    def test_budget_exhausted_undecided(self):
        inst = Instance(
            [Job("x", "gpu", 6, 3, 3), Job("y", "gpu", 4, 10, 100)],
            capacity=10, startup_cost=25,
        )
        sol = solve(inst, transition_budget=4)
        self.assertEqual(sol.status, Status.UNDECIDED)
        self.assertIsNone(sol.total_cost)
        # 同一实例预算充足时可证最优
        self.assertEqual(solve(inst).status, Status.OPTIMAL)

    def test_state_budget_triggers_fallback(self):
        inst = Instance(
            [Job(f"j{k}", "t" + str(k % 2), 1, 1, 100) for k in range(12)],
            capacity=4, startup_cost=3,
        )
        sol = solve(inst, state_budget=1 << 8)  # 2^12 个状态超预算
        self.assertIn(sol.status, (Status.FEASIBLE, Status.UNDECIDED))


class TestAgainstBruteForce(unittest.TestCase):
    def test_random_small_instances_match_brute_force(self):
        rng = random.Random(7)
        for case in range(40):
            n = rng.randint(1, 7)
            jobs = [
                Job(
                    f"j{k}",
                    rng.choice(["a", "b", "c"]),
                    rng.randint(1, 8),
                    rng.randint(1, 5),
                    rng.randint(3, 30),
                )
                for k in range(n)
            ]
            inst = Instance(jobs, capacity=rng.randint(5, 10),
                            startup_cost=rng.randint(1, 5))
            expected = brute_force_min_batches(inst)
            sol = solve(inst)
            if expected is None:
                self.assertEqual(sol.status, Status.INFEASIBLE, f"case {case}")
            else:
                self.assertEqual(sol.status, Status.OPTIMAL, f"case {case}")
                self.assertEqual(sol.num_batches, expected, f"case {case}")


if __name__ == "__main__":
    unittest.main()
