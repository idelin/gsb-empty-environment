"""Tests for reservoir_sampling.

Two kinds of conclusions, kept strictly separate:

* EXACT  -- exhaustive enumeration of every possible random choice on
  small streams (all n! distinct-key assignments). These tests prove
  properties, they do not estimate them.
* EMPIRICAL -- frequency checks with a seeded PRNG. These are statistical
  estimates with an explicit tolerance, not proofs.
"""

import itertools
import math
import random
import unittest

from reservoir_sampling import ReservoirSample


# ----------------------------------------------------------------------
# Helpers
# ----------------------------------------------------------------------
def rng_from_keys(keys):
    """Inject a predetermined key sequence as the random source."""
    it = iter(keys)
    return lambda: next(it)


def central_sample(k, keys):
    s = ReservoirSample(k, shard_id="central", rng=rng_from_keys(keys))
    s.add_all(range(len(keys)))
    return s


def shard_samples(k, sizes, keys):
    """Feed items 0..n-1 into shards of the given sizes.

    Shard i receives the next ``sizes[i]`` items and the corresponding
    slice of the global key sequence, so the sharded run sees exactly the
    same (item, key) pairs as the central run.
    """
    shards = []
    pos = 0
    for i, size in enumerate(sizes):
        s = ReservoirSample(
            k, shard_id=f"shard-{i}", rng=rng_from_keys(keys[pos : pos + size])
        )
        s.add_all(range(pos, pos + size))
        shards.append(s)
        pos += size
    return shards


def merge_left(shards):
    acc = ReservoirSample(shards[0].k, shard_id="tree", seed=0)
    for s in shards:
        acc.merge(s)
    return acc


def merge_right(shards):
    acc = ReservoirSample(shards[0].k, shard_id="tree", seed=0)
    for s in reversed(shards):
        acc.merge(s)
    return acc


def merge_balanced(shards):
    level = list(shards)
    while len(level) > 1:
        nxt = []
        for i in range(0, len(level), 2):
            if i + 1 < len(level):
                m = ReservoirSample(level[i].k, shard_id="tree", seed=0)
                m.merge(level[i]).merge(level[i + 1])
                nxt.append(m)
            else:
                nxt.append(level[i])
        level = nxt
    return level[0]


MERGE_TREES = (merge_left, merge_right, merge_balanced)


def keyed_values(summary):
    return tuple((ki.value, ki.key) for ki in summary.keyed_sample())


# ----------------------------------------------------------------------
# EXACT: exhaustive enumeration of all random choices
# ----------------------------------------------------------------------
class ExactUniformityTest(unittest.TestCase):
    """EXACT conclusions from enumerating all n! key assignments."""

    def test_every_k_subset_equiprobable_central(self):
        # EXACT: over all 5! = 120 key assignments, each of the C(5,2)=10
        # subsets must appear exactly 120/10 = 12 times.
        n, k = 5, 2
        base = [(i + 1) / (n + 1) for i in range(n)]
        counts = {}
        perms = list(itertools.permutations(base))
        self.assertEqual(len(perms), math.factorial(n))
        for perm in perms:
            subset = tuple(sorted(central_sample(k, perm).sample()))
            counts[subset] = counts.get(subset, 0) + 1
        self.assertEqual(len(counts), math.comb(n, k))
        expected = math.factorial(n) // math.comb(n, k)
        for subset, count in counts.items():
            self.assertEqual(
                count, expected, f"subset {subset} appeared {count} times"
            )

    def test_merge_trees_match_central_exactly(self):
        # EXACT: for every key assignment, every shard layout (including
        # very unequal shard sizes) and every merge-tree shape, the merged
        # keyed sample is identical to the central one. Hence per-item
        # inclusion probability is 1/n regardless of shard sizes.
        n, k = 6, 3
        base = [(i + 1) / (n + 1) for i in range(n)]
        layouts = [[6], [3, 3], [1, 2, 3], [2, 2, 2], [1, 1, 1, 1, 1, 1], [5, 1]]
        for perm in itertools.permutations(base):
            expected = keyed_values(central_sample(k, perm))
            for sizes in layouts:
                shards = shard_samples(k, sizes, perm)
                for tree in MERGE_TREES:
                    self.assertEqual(keyed_values(tree(shards)), expected)

    def test_hierarchical_merge_then_append_matches_central(self):
        # EXACT: a merged summary can keep appending; the result still
        # equals the central run over the full stream.
        n, k = 5, 2
        base = [(i + 1) / (n + 1) for i in range(n)]
        for perm in itertools.permutations(base):
            expected = keyed_values(central_sample(k, perm))
            shards = shard_samples(k, [2, 2], perm[:4])
            merged = ReservoirSample.merged(
                shards, shard_id="m", rng=rng_from_keys([perm[4]])
            )
            merged.add(n - 1)
            self.assertEqual(keyed_values(merged), expected)

    def test_retry_merge_is_idempotent(self):
        # EXACT: re-merging the same summaries (retry, diamond topology)
        # never changes the sample or the total length.
        keys = [0.5, 0.1, 0.9, 0.3, 0.7]
        shards = shard_samples(2, [2, 3], keys)
        acc = ReservoirSample(2, shard_id="agg", seed=0)
        acc.merge(shards[0]).merge(shards[1])
        snapshot = (acc.keyed_sample(), acc.n)
        for _ in range(3):
            acc.merge(shards[0]).merge(shards[1])
        self.assertEqual((acc.keyed_sample(), acc.n), snapshot)

        left = ReservoirSample.merged([shards[0]], shard_id="L", seed=0)
        right = ReservoirSample.merged([shards[1]], shard_id="R", seed=0)
        root_a = ReservoirSample.merged([left, right], shard_id="root", seed=0)
        root_b = ReservoirSample.merged([right, left], shard_id="root", seed=0)
        root_a.merge(root_b)  # diamond: same data arrives via two paths
        self.assertEqual(root_a.keyed_sample(), root_b.keyed_sample())
        self.assertEqual(root_a.n, 5)
        self.assertEqual(keyed_values(root_a), keyed_values(central_sample(2, keys)))


# ----------------------------------------------------------------------
# Stream semantics
# ----------------------------------------------------------------------
class StreamSemanticsTest(unittest.TestCase):
    def test_short_stream_returns_everything(self):
        s = ReservoirSample(5, shard_id="s", seed=7)
        s.add_all(["a", "b", "c"])
        self.assertEqual(sorted(s.sample()), ["a", "b", "c"])
        self.assertEqual(s.n, 3)
        self.assertEqual(s.sample_size, 3)

    def test_empty_stream(self):
        s = ReservoirSample(3, shard_id="s", seed=7)
        self.assertEqual(s.sample(), [])
        self.assertEqual(s.n, 0)

    def test_k_zero_stores_nothing(self):
        s = ReservoirSample(0, shard_id="z", seed=1)
        s.add_all(range(100))
        self.assertEqual(s.n, 100)
        self.assertEqual(s.sample(), [])
        self.assertEqual(s.sample_size, 0)
        self.assertEqual(s.rng_draws, 0)  # no random keys drawn, nothing kept
        other = ReservoirSample(0, shard_id="w", seed=2)
        other.add_all(range(50))
        s.merge(other)
        self.assertEqual(s.n, 150)
        self.assertEqual(s.sample(), [])

    def test_invalid_k_rejected(self):
        with self.assertRaises(ValueError):
            ReservoirSample(-1, shard_id="s")

    def test_rng_out_of_range_rejected(self):
        s = ReservoirSample(1, shard_id="s", rng=lambda: 1.5)
        with self.assertRaises(ValueError):
            s.add("x")

    def test_merge_different_k_rejected(self):
        a = ReservoirSample(2, shard_id="a", seed=1)
        b = ReservoirSample(3, shard_id="b", seed=1)
        with self.assertRaises(ValueError):
            a.merge(b)

    def test_metadata_records_seed_and_sizes(self):
        s = ReservoirSample(4, shard_id="s", seed=20260927)
        s.add_all(range(10))
        meta = s.metadata()
        self.assertEqual(meta["seed"], 20260927)
        self.assertEqual(meta["k"], 4)
        self.assertEqual(meta["n"], 10)
        self.assertEqual(meta["sample_size"], 4)
        self.assertEqual(meta["rng_draws"], 10)
        self.assertEqual(meta["shard_lengths"], {"s": 10})


# ----------------------------------------------------------------------
# Length corrections and retractions (capability boundaries)
# ----------------------------------------------------------------------
class CorrectionRetractionTest(unittest.TestCase):
    def test_correct_length_updates_count_not_sample(self):
        s = ReservoirSample(3, shard_id="a", seed=11)
        s.add_all(range(10))
        before = s.keyed_sample()
        s.correct_length("a", 8)
        self.assertEqual(s.n, 8)
        self.assertEqual(s.keyed_sample(), before)  # sample unaffected

    def test_correct_length_merged_shards(self):
        shards = shard_samples(2, [4, 6], [(i + 1) / 11 for i in range(10)])
        merged = ReservoirSample.merged(shards, shard_id="m", seed=0)
        self.assertEqual(merged.n, 10)
        merged.correct_length("shard-0", 3)
        self.assertEqual(merged.n, 9)

    def test_correct_length_boundaries(self):
        s = ReservoirSample(3, shard_id="a", seed=11)
        s.add_all(range(10))
        with self.assertRaises(KeyError):
            s.correct_length("unknown", 5)
        with self.assertRaises(ValueError):
            s.correct_length("a", -1)
        retained = s.sample_size  # 3 items of shard "a" are held
        with self.assertRaises(ValueError):
            s.correct_length("a", retained - 1)

    def test_correction_is_local_stale_merge_wins(self):
        # Documented boundary: shard lengths merge append-only (max), so a
        # local correction is overwritten by a later merge that carries a
        # larger stale length for the same shard.
        stale = ReservoirSample(2, shard_id="a", seed=3)
        stale.add_all(range(10))
        agg = ReservoirSample.merged([stale], shard_id="agg", seed=0)
        agg.correct_length("a", 7)
        self.assertEqual(agg.n, 7)
        agg.merge(stale)  # stale copy still reports length 10
        self.assertEqual(agg.n, 10)

    def test_retract_held_item(self):
        s = ReservoirSample(3, shard_id="a", rng=rng_from_keys(
            [0.9, 0.1, 0.8, 0.2, 0.7]))
        s.add_all(range(5))
        held_identities = {ki.identity for ki in s.keyed_sample()}
        target = next(iter(held_identities))
        self.assertTrue(s.retract(target))
        self.assertEqual(s.n, 4)
        self.assertEqual(s.sample_size, 2)  # evicted items cannot be restored
        self.assertNotIn(target, {ki.identity for ki in s.keyed_sample()})

    def test_retract_evicted_item(self):
        s = ReservoirSample(1, shard_id="a", rng=rng_from_keys([0.1, 0.9]))
        s.add_all(range(2))  # item 1 (key 0.9) is evicted immediately
        self.assertFalse(s.retract(("a", 1)))  # not held, but length drops
        self.assertEqual(s.n, 1)
        self.assertEqual(s.sample(), [0])

    def test_retract_boundaries(self):
        s = ReservoirSample(2, shard_id="a", seed=5)
        s.add_all(range(3))
        with self.assertRaises(KeyError):
            s.retract(("unknown", 0))
        s.retract(("a", 0))
        s.retract(("a", 1))
        s.retract(("a", 2))
        with self.assertRaises(ValueError):
            s.retract(("a", 0))  # length already zero


# ----------------------------------------------------------------------
# EMPIRICAL: seeded-PRNG frequency estimates (not proofs)
# ----------------------------------------------------------------------
class EmpiricalUniformityTest(unittest.TestCase):
    def test_seeded_prng_subset_frequencies(self):
        # EMPIRICAL estimate: 60_000 seeded trials, each C(6,3)=20 subsets
        # expected 3_000 times; 5-sigma binomial tolerance (~+-271).
        n, k, trials = 6, 3, 60_000
        counts = {}
        for t in range(trials):
            s = ReservoirSample(k, shard_id="s", seed=t)
            s.add_all(range(n))
            subset = tuple(sorted(s.sample()))
            counts[subset] = counts.get(subset, 0) + 1
        self.assertEqual(len(counts), math.comb(n, k))
        p = 1 / math.comb(n, k)
        expected = trials * p
        sigma = math.sqrt(trials * p * (1 - p))
        for subset, count in counts.items():
            self.assertLess(
                abs(count - expected),
                5 * sigma,
                f"subset {subset}: {count} vs expected {expected:.0f}",
            )

    def test_merged_matches_central_with_random_keys(self):
        # EMPIRICAL setting (random keys), EXACT per-trial equality:
        # sharded+merged must equal central for every random key sequence.
        rng = random.Random(20260927)
        for _ in range(2_000):
            keys = [rng.random() for _ in range(9)]
            expected = keyed_values(central_sample(4, keys))
            shards = shard_samples(4, [2, 3, 4], keys)
            self.assertEqual(keyed_values(merge_balanced(shards)), expected)


if __name__ == "__main__":
    unittest.main(verbosity=2)
