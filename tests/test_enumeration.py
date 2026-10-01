"""小 n 枚举：精确证明各排列等概率；另附标注为经验估计的频率检验。"""
import itertools
import math
import unittest
from collections import Counter

from unbiased_shuffle import CountingSource, ShuffleState


class ScriptedSource:
    """按脚本的 j 序列驱动的随机源：j < i+1 时 randbelow 必接受。"""

    def __init__(self, js):
        self.js = list(js)

    def getrandbits(self, k):
        return self.js.pop(0)


def shuffle_with_choices(n, js):
    state = ShuffleState(list(range(n)), ScriptedSource(js))
    return tuple(state.run())


class TestExactUniformity(unittest.TestCase):
    """【精确结论】枚举全部随机分支：n! 个分支与 n! 个排列一一对应。"""

    def test_all_branches_give_distinct_permutations(self):
        for n in range(1, 7):
            counts = Counter()
            branch_spaces = [range(i + 1) for i in range(n - 1, 0, -1)]
            total_branches = 1
            for space in branch_spaces:
                total_branches *= len(space)
            self.assertEqual(total_branches, math.factorial(n))  # 分支数恰为 n!
            for js in itertools.product(*branch_spaces):
                counts[shuffle_with_choices(n, js)] += 1
            # 每个排列恰好出现一次：j 均匀 => 排列严格均匀（双射证明）
            self.assertEqual(len(counts), math.factorial(n))
            self.assertTrue(all(c == 1 for c in counts.values()))

    def test_branch_js_within_range(self):
        # 每步分支空间恰为 [0, i]，与 randbelow(i+1) 的接受域一致
        n = 5
        for js in itertools.product(*[range(i + 1) for i in range(n - 1, 0, -1)]):
            for i, j in zip(range(n - 1, 0, -1), js):
                self.assertTrue(0 <= j <= i)


class TestEmpiricalUniformity(unittest.TestCase):
    """【经验估计】固定 seed 的频率检验，非严格证明。"""

    def test_empirical_frequency_n4(self):
        n, trials = 4, 96000
        src = CountingSource(seed=20261001)
        counts = Counter()
        for _ in range(trials):
            counts[tuple(ShuffleState(list(range(n)), src).run())] += 1
        self.assertEqual(len(counts), math.factorial(n))
        expected = trials / math.factorial(n)  # 4000
        # 单格标准差 ≈ sqrt(4000 * 23/24) ≈ 61.9；容差 ±480 ≈ 7.7σ
        for perm, c in counts.items():
            self.assertAlmostEqual(c, expected, delta=480,
                                   msg=f"empirical frequency off for {perm}")


if __name__ == "__main__":
    unittest.main()
