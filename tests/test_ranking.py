"""按排名定位（部分）排列：双射的精确验证与大整数索引。"""
import math
import unittest

from unbiased_shuffle import (
    num_partial_permutations,
    rank_partial,
    rank_permutation,
    unrank_partial,
    unrank_permutation,
)


class TestPermutationRanking(unittest.TestCase):
    """【精确结论】rank/unrank 是 [0, n!) 与全体排列之间的双射。"""

    def test_full_permutation_bijection(self):
        for n in range(1, 8):
            seen = set()
            for rank in range(math.factorial(n)):
                perm = tuple(unrank_permutation(n, rank))
                self.assertEqual(sorted(perm), list(range(n)))
                seen.add(perm)
                self.assertEqual(rank_permutation(perm), rank)
            self.assertEqual(len(seen), math.factorial(n))  # 互不相同 => 双射

    def test_partial_permutation_bijection(self):
        for n, k in [(1, 1), (3, 2), (5, 3), (7, 4), (8, 8), (6, 0)]:
            total = num_partial_permutations(n, k)
            self.assertEqual(total, math.perm(n, k))
            seen = set()
            for rank in range(total):
                partial = tuple(unrank_partial(n, k, rank))
                self.assertEqual(len(set(partial)), k)
                self.assertTrue(all(0 <= v < n for v in partial))
                seen.add(partial)
                self.assertEqual(rank_partial(n, partial), rank)
            self.assertEqual(len(seen), total)

    def test_rank_bounds(self):
        with self.assertRaises(ValueError):
            unrank_permutation(5, math.factorial(5))
        with self.assertRaises(ValueError):
            unrank_permutation(5, -1)
        with self.assertRaises(ValueError):
            unrank_partial(4, 5, 0)


class TestBigIntegerRanking(unittest.TestCase):
    """大整数索引：rank 与 n 均可远超机器字长。"""

    def test_huge_rank_roundtrip(self):
        n = 1000  # 1000! ≈ 4.02e2567
        rank = 2 ** 2500 + 987654321
        perm = unrank_permutation(n, rank)
        self.assertEqual(sorted(perm), list(range(n)))
        self.assertEqual(rank_permutation(perm), rank)

    def test_astronomical_n_partial(self):
        n = 10 ** 60  # 不可能物化候选列表
        k = 6
        total = num_partial_permutations(n, k)
        rank = total - 12345678901234567890
        partial = unrank_partial(n, k, rank)
        self.assertEqual(len(set(partial)), k)
        self.assertTrue(all(0 <= v < n for v in partial))
        self.assertEqual(rank_partial(n, partial), rank)

    def test_unrank_matches_lehmer_of_small(self):
        # 与朴素 Lehmer 码对照（小 n）
        def naive_unrank(n, rank):
            available = list(range(n))
            out = []
            for i in range(n):
                f = math.factorial(n - 1 - i)
                idx, rank = divmod(rank, f)
                out.append(available.pop(idx))
            return out

        for rank in range(math.factorial(6)):
            self.assertEqual(unrank_permutation(6, rank), naive_unrank(6, rank))


if __name__ == "__main__":
    unittest.main()
