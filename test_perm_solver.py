import random
import unittest

from perm_solver import (
    PermSolver,
    brute_force_solutions,
    check_solution,
    inversion_count,
    min_max_inversions,
    propagate,
)


def null_log(msg):
    pass


class TestInversionCount(unittest.TestCase):
    def test_fixed_example(self):
        # 固定排列 [3,1,2] 的逆序对为 (3,1)、(3,2)，逆序数 = 2
        self.assertEqual(inversion_count([3, 1, 2]), 2)

    def test_extremes(self):
        self.assertEqual(inversion_count([1, 2, 3, 4]), 0)
        self.assertEqual(inversion_count([4, 3, 2, 1]), 6)


class TestMinMaxInversions(unittest.TestCase):
    def test_full_domains(self):
        values = [1, 2, 3, 4]
        domains = [set(values) for _ in range(4)]
        self.assertEqual(min_max_inversions(domains, values), (0, 6))

    def test_infeasible_alldifferent(self):
        # 两个变量共享同一个单值，allDifferent 不可满足
        domains = [{1}, {1}, {1, 2, 3}]
        self.assertIsNone(min_max_inversions(domains, [1, 2, 3]))

    def test_forced_assignment(self):
        domains = [{3}, {1}, {2}]
        self.assertEqual(min_max_inversions(domains, [1, 2, 3]), (2, 2))


class TestContradictoryBounds(unittest.TestCase):
    def test_low_greater_than_high_fails_immediately(self):
        solver = PermSolver([{1, 2, 3}] * 3, low=2, high=1)
        self.assertIsNone(solver.solve())
        self.assertTrue(any("立即失败" in m for m in solver.trace))
        self.assertEqual(solver.count_solutions(), 0)

    def test_bounds_beyond_reachable_range_fail(self):
        # n=3 最大逆序数为 3，要求 >=4 必然失败
        solver = PermSolver([{1, 2, 3}] * 3, low=4, high=6)
        self.assertIsNone(solver.solve())
        self.assertTrue(any("失败" in m for m in solver.trace))

    def test_exact_bound_on_fixed_perm(self):
        # 强制 x0=3,x1=1,x2=2 后逆序数恰为 2；区间 [2,2] 有解，[0,1] 无解
        domains = [{3}, {1}, {2}]
        sol = PermSolver(domains, 2, 2).solve()
        self.assertEqual(sol, [3, 1, 2])
        self.assertIsNone(PermSolver(domains, 0, 1).solve())


class TestIndependentChecker(unittest.TestCase):
    def setUp(self):
        self.values = [1, 2, 3]
        self.domains = [set(self.values) for _ in range(3)]

    def test_accepts_valid(self):
        self.assertTrue(check_solution([3, 1, 2], self.domains, self.values, 2, 2))

    def test_rejects_wrong_inversion_count(self):
        self.assertFalse(check_solution([3, 1, 2], self.domains, self.values, 0, 1))

    def test_rejects_non_permutation(self):
        self.assertFalse(check_solution([1, 1, 2], self.domains, self.values, 0, 6))

    def test_rejects_value_outside_original_domain(self):
        domains = [{1, 2}, {1, 2, 3}, {1, 2, 3}]
        self.assertFalse(check_solution([3, 1, 2], domains, self.values, 0, 6))


class TestPruningAgainstBruteForce(unittest.TestCase):
    """小规模随机实例：传播后的域必须与穷举解集在每个变量上的投影一致。

    这同时证明两个方向：
    - 安全性：仍有合法完成方案的值不会被删除；
    - 完备性：凡被保留的值都确实出现在某个合法完成方案中。
    """

    def test_random_instances(self):
        rng = random.Random(20260927)
        for trial in range(80):
            n = rng.randint(2, 5)
            values = list(range(1, n + 1))
            domains = [set(rng.sample(values, rng.randint(1, n))) for _ in range(n)]
            max_inv = n * (n - 1) // 2
            low = rng.randint(0, max_inv)
            high = rng.randint(low, max_inv)
            sols = brute_force_solutions(domains, values, low, high)
            result = propagate(domains, values, low, high, null_log)
            if not sols:
                self.assertIsNone(
                    result, f"trial {trial}: 穷举无解但传播未判失败 "
                            f"domains={domains} [{low},{high}]")
            else:
                self.assertIsNotNone(
                    result, f"trial {trial}: 穷举有解但传播判失败 "
                            f"domains={domains} [{low},{high}]")
                for i in range(n):
                    expected = {s[i] for s in sols}
                    self.assertEqual(
                        result[i], expected,
                        f"trial {trial}: x{i} 传播结果 {sorted(result[i])} "
                        f"!= 穷举投影 {sorted(expected)} "
                        f"domains={domains} [{low},{high}]")


class TestSolverAgainstBruteForce(unittest.TestCase):
    def test_solve_and_count_match_brute_force(self):
        rng = random.Random(112233)
        for trial in range(60):
            n = rng.randint(2, 5)
            values = list(range(1, n + 1))
            domains = [set(rng.sample(values, rng.randint(1, n))) for _ in range(n)]
            max_inv = n * (n - 1) // 2
            low = rng.randint(0, max_inv)
            high = rng.randint(low, max_inv)
            sols = brute_force_solutions(domains, values, low, high)

            solver = PermSolver(domains, low, high)
            sol = solver.solve()
            if not sols:
                self.assertIsNone(sol, f"trial {trial}")
            else:
                self.assertIsNotNone(sol, f"trial {trial}")
                self.assertIn(sol, sols, f"trial {trial}: 解不在穷举解集中")
                self.assertTrue(
                    check_solution(sol, domains, values, low, high),
                    f"trial {trial}: 独立检查器拒绝求解器给出的解")

            counter = PermSolver(domains, low, high)
            self.assertEqual(counter.count_solutions(), len(sols),
                             f"trial {trial}: 解计数与穷举不一致")

    def test_known_instance(self):
        # n=3 逆序数恰为 2 的排列只有 [2,3,1] 与 [3,1,2]
        domains = [{1, 2, 3}] * 3
        self.assertEqual(PermSolver(domains, 2, 2).count_solutions(), 2)
        sol = PermSolver(domains, 2, 2).solve()
        self.assertIn(sol, ([2, 3, 1], [3, 1, 2]))


class TestBacktrackingRestoresDomains(unittest.TestCase):
    def test_original_domains_untouched_after_search(self):
        domains = [{1, 3, 4}, {1, 2, 3, 4}, {2, 4}, {1, 2, 3, 4}]
        snapshot = [set(d) for d in domains]
        solver = PermSolver(domains, 2, 4)
        solver.count_solutions()
        self.assertEqual(solver.orig_domains, snapshot)
        self.assertEqual(domains, snapshot)

    def test_repeated_solves_are_consistent(self):
        domains = [{1, 2, 3, 4}] * 4
        solver = PermSolver(domains, 3, 3)
        first = solver.solve()
        second = solver.solve()
        self.assertEqual(first, second)
        self.assertIsNotNone(first)

    def test_propagate_does_not_mutate_input(self):
        domains = [{1, 2, 3}, {1, 2, 3}, {1, 2, 3}]
        snapshot = [set(d) for d in domains]
        propagate(domains, [1, 2, 3], 1, 1, null_log)
        self.assertEqual(domains, snapshot)


class TestPruneLogging(unittest.TestCase):
    def test_every_prune_has_reason(self):
        domains = [{1, 2, 3, 4}] * 4
        solver = PermSolver(domains, 0, 0)  # 只允许升序排列
        solver.solve()
        prunes = [m for m in solver.trace if m.startswith("剪枝")]
        self.assertTrue(prunes, "应当产生剪枝记录")
        for m in prunes:
            self.assertIn("（", m)  # 每条剪枝都附依据
        self.assertTrue(any("传播" in m for m in solver.trace))
        self.assertTrue(any("独立检查器验证通过" in m for m in solver.trace))


if __name__ == "__main__":
    unittest.main(verbosity=2)
