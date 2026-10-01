"""Tests for rank/unrank of partial permutations (positioning by rank)."""

import itertools
import unittest

from unbiased_shuffle import (
    num_partial_permutations,
    rank_partial,
    unrank_partial,
)


class EnumerationTest(unittest.TestCase):
    """EXACT: unrank enumerates every k-permutation exactly once."""

    def test_unrank_is_bijection_for_small_nk(self):
        for n in range(0, 7):
            for k in range(0, n + 1):
                total = num_partial_permutations(n, k)
                seen = [tuple(unrank_partial(n, k, r)) for r in range(total)]
                self.assertEqual(len(set(seen)), total)
                self.assertEqual(
                    set(seen), set(itertools.permutations(range(n), k))
                )
                for r, perm in enumerate(seen):
                    self.assertEqual(rank_partial(n, k, perm), r)

    def test_lexicographic_order(self):
        perms = [unrank_partial(4, 2, r) for r in range(12)]
        expected = sorted(map(list, itertools.permutations(range(4), 2)))
        self.assertEqual(perms, expected)


class BigIntegerTest(unittest.TestCase):
    """Huge n, k and ranks are exact big-integer arithmetic."""

    def test_roundtrip_huge_rank(self):
        n, k = 1000, 500
        total = num_partial_permutations(n, k)
        self.assertGreater(total, 10**1000)
        for rank in (0, 1, total // 2, total - 1):
            perm = unrank_partial(n, k, rank)
            self.assertEqual(len(perm), k)
            self.assertEqual(len(set(perm)), k)
            self.assertEqual(rank_partial(n, k, perm), rank)

    def test_invalid_inputs(self):
        with self.assertRaises(ValueError):
            unrank_partial(5, 2, num_partial_permutations(5, 2))
        with self.assertRaises(ValueError):
            unrank_partial(5, 2, -1)
        with self.assertRaises(ValueError):
            num_partial_permutations(3, 4)
        with self.assertRaises(ValueError):
            rank_partial(5, 2, [1, 1])       # duplicate element
        with self.assertRaises(ValueError):
            rank_partial(5, 2, [1, 7])       # out of range
        with self.assertRaises(ValueError):
            rank_partial(5, 3, [1, 2])       # wrong length


if __name__ == "__main__":
    unittest.main()
