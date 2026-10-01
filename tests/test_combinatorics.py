import itertools
import unittest
from math import comb

from permtest import iter_combination_shard, shard_ranges, unrank_combination


class TestShardRanges(unittest.TestCase):
    def test_cover_without_gap_or_overlap(self):
        for total in [0, 1, 2, 7, 100]:
            for num_shards in [1, 2, 3, 5, 13]:
                ranges = shard_ranges(total, num_shards)
                covered = [r for start, stop in ranges for r in range(start, stop)]
                self.assertEqual(covered, list(range(total)))

    def test_sizes_differ_by_at_most_one(self):
        ranges = shard_ranges(10, 3)
        sizes = [stop - start for start, stop in ranges]
        self.assertEqual(sizes, [4, 3, 3])

    def test_invalid(self):
        with self.assertRaises(ValueError):
            shard_ranges(5, 0)


class TestUnrank(unittest.TestCase):
    def test_matches_itertools_order(self):
        for n, k in [(5, 0), (5, 1), (5, 3), (6, 6), (8, 4)]:
            expected = list(itertools.combinations(range(n), k))
            for rank, combo in enumerate(expected):
                self.assertEqual(tuple(unrank_combination(n, k, rank)), combo)

    def test_out_of_range(self):
        with self.assertRaises(ValueError):
            unrank_combination(4, 2, comb(4, 2))


class TestShardEnumeration(unittest.TestCase):
    def test_shards_reconstruct_full_enumeration(self):
        # No duplicates, no omissions: concatenated shards equal the
        # reference itertools enumeration exactly, in order.
        for n, k in [(6, 3), (7, 0), (7, 7), (9, 4)]:
            reference = list(itertools.combinations(range(n), k))
            for num_shards in [1, 2, 3, 7, len(reference) + 5]:
                total = comb(n, k)
                gathered = []
                for start, stop in shard_ranges(total, num_shards):
                    gathered.extend(iter_combination_shard(n, k, start, stop))
                self.assertEqual(gathered, reference)

    def test_empty_shard_yields_nothing(self):
        self.assertEqual(list(iter_combination_shard(5, 2, 3, 3)), [])

    def test_bad_bounds_rejected(self):
        with self.assertRaises(ValueError):
            list(iter_combination_shard(5, 2, 4, 3))
        with self.assertRaises(ValueError):
            list(iter_combination_shard(5, 2, 0, comb(5, 2) + 1))


if __name__ == "__main__":
    unittest.main()
