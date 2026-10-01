import itertools
import json
import math
import tempfile
import unittest
from pathlib import Path

from permtest.exact import (
    ExactAccumulator,
    accumulate_shard,
    exact_test,
    is_extreme,
    shard_ranges,
)
from permtest.spaces import (
    IndependentTwoSampleSpace,
    PairedSwapSpace,
    StratifiedSpace,
    unrank_combination,
)


class TestUnrankCombination(unittest.TestCase):
    def test_matches_itertools_order(self):
        for n, k in [(5, 2), (6, 3), (7, 0), (4, 4)]:
            expected = list(itertools.combinations(range(n), k))
            for rank, combo in enumerate(expected):
                self.assertEqual(unrank_combination(n, k, rank), combo)

    def test_rank_out_of_range(self):
        with self.assertRaises(ValueError):
            unrank_combination(4, 2, 6)


class TestTailRules(unittest.TestCase):
    def test_two_sided_includes_equality(self):
        self.assertTrue(is_extreme(2.0, 2.0, "two-sided", 0.0))
        self.assertTrue(is_extreme(-2.0, 2.0, "two-sided", 0.0))
        self.assertFalse(is_extreme(1.999, 2.0, "two-sided", 0.0))

    def test_one_sided_includes_equality(self):
        self.assertTrue(is_extreme(2.0, 2.0, "greater", 0.0))
        self.assertTrue(is_extreme(2.0, 2.0, "less", 0.0))
        self.assertFalse(is_extreme(1.0, 2.0, "greater", 0.0))

    def test_unknown_alternative(self):
        with self.assertRaises(ValueError):
            is_extreme(1.0, 1.0, "sideways", 0.0)


class TestExactIndependent(unittest.TestCase):
    def test_known_example(self):
        # values [3, 4 | 1, 2]; observed diff of means = 2.0
        # subsets with |diff| >= 2.0: {3,4} and {1,2} -> p = 2/6
        space = IndependentTwoSampleSpace([3, 4, 1, 2], 2)
        result = exact_test(space)
        self.assertEqual(result.n_total, 6)
        self.assertEqual(result.n_extreme, 2)
        self.assertAlmostEqual(result.p_value, 1 / 3)
        self.assertEqual(result.method, "exact (full enumeration)")

    def test_original_arrangement_included(self):
        space = IndependentTwoSampleSpace([5, 6, 1, 2, 3], 2)
        result = exact_test(space)
        self.assertGreaterEqual(result.n_extreme, 1)
        self.assertGreaterEqual(result.p_value, 1 / result.n_total)

    def test_unequal_group_sizes(self):
        space = IndependentTwoSampleSpace([10, 11, 12, 1, 2], 3)
        self.assertEqual(space.size, math.comb(5, 3))
        result = exact_test(space, alternative="greater")
        self.assertEqual(result.n_total, 10)
        self.assertEqual(result.n_extreme, 1)  # only the observed split is this extreme
        self.assertAlmostEqual(result.p_value, 0.1)

    def test_ties_keep_multiplicity(self):
        # Three identical values: the space size must still be C(4, 2) = 6,
        # not the number of distinct value assignments.
        space = IndependentTwoSampleSpace([1, 1, 1, 2], 2)
        result = exact_test(space)
        self.assertEqual(result.n_total, 6)
        # observed: group1 = {1a, 1b}, diff = 1 - 1.5 = -0.5.
        # Every identity subset has |diff| = 0.5 (three {1,1} picks and
        # three {1,2} picks), so all 6 are extreme -- duplicates are counted
        # with full multiplicity instead of collapsing to 2 distinct cases.
        self.assertEqual(result.n_extreme, 6)
        self.assertAlmostEqual(result.p_value, 1.0)

    def test_two_sided_counts_mirror_image(self):
        # values [1, 2 | 3, 4]: observed diff = -2; mirror {3,4} gives +2
        # and must be counted (equality on the two-sided threshold).
        space = IndependentTwoSampleSpace([1, 2, 3, 4], 2)
        result = exact_test(space)
        self.assertEqual(result.n_extreme, 2)
        self.assertAlmostEqual(result.p_value, 2 / 6)


class TestShardingAndResume(unittest.TestCase):
    def setUp(self):
        self.space = IndependentTwoSampleSpace([9, 8, 7, 1, 2, 3, 4], 3)
        self.observed = self.space.statistic(self.space.observed_rank())

    def test_shard_ranges_cover_exactly(self):
        ranges = list(shard_ranges(self.space.size, 4))
        self.assertEqual(ranges[0][0], 0)
        self.assertEqual(ranges[-1][1], self.space.size)
        for (a, b), (c, d) in zip(ranges, ranges[1:]):
            self.assertEqual(b, c)

    def test_sharded_equals_unsharded(self):
        whole = exact_test(self.space, n_shards=1)
        sharded = exact_test(self.space, n_shards=5)
        self.assertEqual(whole.n_extreme, sharded.n_extreme)
        self.assertEqual(whole.n_total, sharded.n_total)

    def test_manual_accumulator_merge(self):
        acc = ExactAccumulator()
        for start, end in shard_ranges(self.space.size, 3):
            acc = accumulate_shard(
                self.space, self.observed, "two-sided", start, end, acc
            )
        whole = exact_test(self.space)
        self.assertEqual(acc.n_extreme, whole.n_extreme)
        self.assertEqual(acc.n_total, whole.n_total)

    def test_checkpoint_resume(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = str(Path(tmp) / "ckpt.json")
            full = exact_test(self.space, n_shards=6)

            # Simulate an interrupted run: process only the first 2 shards.
            ranges = list(shard_ranges(self.space.size, 6))
            acc = ExactAccumulator()
            for start, end in ranges[:2]:
                acc = accumulate_shard(
                    self.space, self.observed, "two-sided", start, end, acc
                )
            Path(path).write_text(json.dumps({
                "design": self.space.design,
                "alternative": "two-sided",
                "next_shard": 2,
                "n_shards": 6,
                "accumulator": acc.to_dict(),
            }))

            resumed = exact_test(
                self.space, n_shards=6, checkpoint_path=path, resume=True
            )
            self.assertEqual(resumed.n_extreme, full.n_extreme)
            self.assertEqual(resumed.n_total, full.n_total)

    def test_checkpoint_mismatch_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = str(Path(tmp) / "ckpt.json")
            exact_test(self.space, n_shards=2, checkpoint_path=path)
            with self.assertRaises(ValueError):
                exact_test(
                    self.space, alternative="greater",
                    n_shards=2, checkpoint_path=path, resume=True,
                )


class TestExactPaired(unittest.TestCase):
    def test_space_size(self):
        self.assertEqual(PairedSwapSpace([1, 2, 3]).size, 8)

    def test_known_example(self):
        # diffs [1, 2, 4]; observed mean = 7/3. Sign flips with
        # |mean| >= 7/3: +++ (7/3) and --- (-7/3) -> p = 2/8.
        result = exact_test(PairedSwapSpace([1, 2, 4]))
        self.assertEqual(result.n_total, 8)
        self.assertEqual(result.n_extreme, 2)
        self.assertAlmostEqual(result.p_value, 0.25)


class TestExactStratified(unittest.TestCase):
    def test_space_size_is_product(self):
        space = StratifiedSpace([([3, 1, 2], 1), ([5, 4, 1, 2], 2)])
        self.assertEqual(space.size, math.comb(3, 1) * math.comb(4, 2))

    def test_rank_zero_is_observed(self):
        space = StratifiedSpace([([3, 1, 2], 1), ([5, 4, 1, 2], 2)])
        self.assertEqual(space.observed_rank(), 0)
        result = exact_test(space)
        self.assertGreaterEqual(result.n_extreme, 1)
        self.assertEqual(result.n_total, space.size)


if __name__ == "__main__":
    unittest.main()
