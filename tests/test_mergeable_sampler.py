"""Tests for mergeable_sampler.

Test names and docstrings label each result as EXACT (proved by full
enumeration of the equally likely key orderings) or EMPIRICAL (seeded
Monte-Carlo estimate).
"""

import unittest

from mergeable_sampler import (
    Entry,
    Merger,
    RecordedRandom,
    ScriptedRandom,
    Shard,
    ShardSnapshot,
)
from mergeable_sampler import verify


def scaled_keys(perm):
    """Turn a permutation of range(n) into distinct keys in [0, 1)."""
    n = len(perm)
    return tuple((p + 0.5) / n for p in perm)


class ShardBasicsTest(unittest.TestCase):
    def test_stream_shorter_than_k_returns_all(self):
        shard = Shard("s", k=5, rng=RecordedRandom(1))
        shard.offer_many(["a", "b", "c"])
        self.assertEqual(shard.n, 3)
        self.assertEqual(sorted(shard.sample()), ["a", "b", "c"])
        self.assertEqual(len(shard.snapshot().entries), 3)

    def test_k_zero_retains_nothing(self):
        shard = Shard("s", k=0, rng=RecordedRandom(1))
        shard.offer_many(range(100))
        self.assertEqual(shard.n, 100)  # length is still tracked
        self.assertEqual(shard.sample(), [])  # but no data is stored
        merger = Merger(k=0)
        merger.merge(shard.snapshot())
        self.assertEqual(merger.n, 100)
        self.assertEqual(merger.sample(), [])

    def test_invalid_k_rejected(self):
        with self.assertRaises(ValueError):
            Shard("s", k=-1, rng=RecordedRandom(1))
        with self.assertRaises(ValueError):
            Merger(k=-1)


class ExactEnumerationTest(unittest.TestCase):
    """EXACT results: full enumeration of all n! key orderings."""

    def test_exact_uniformity_centralized(self):
        # EXACT: n=5, k=2 -> each of the 10 subsets appears exactly 12 times.
        ok, counts = verify.check_exact_uniformity(n=5, k=2)
        self.assertTrue(ok)
        self.assertEqual(len(counts), 10)
        self.assertTrue(all(c == 12 for c in counts.values()))

    def test_exact_uniformity_uneven_shards(self):
        # EXACT: shard sizes 1/2/3 -> every 3-subset still equally likely.
        shards = verify.partition(6, [1, 2, 3])
        ok, counts = verify.check_exact_uniformity(n=6, k=3, shards=shards)
        self.assertTrue(ok)
        self.assertEqual(len(counts), 20)
        self.assertTrue(all(c == 36 for c in counts.values()))

    def test_exact_k_covers_edge_cases(self):
        # EXACT: k >= n returns everything; k = 0 returns nothing.
        ok, counts = verify.check_exact_uniformity(n=4, k=4)
        self.assertTrue(ok)
        self.assertEqual(list(counts), [frozenset({0, 1, 2, 3})])
        ok, counts = verify.check_exact_uniformity(n=4, k=0)
        self.assertTrue(ok)
        self.assertEqual(list(counts), [frozenset()])

    def test_sharded_equals_centralized_for_every_ordering(self):
        # EXACT: for every key ordering, one-level merge == centralized.
        import itertools

        for n, k, sizes in [(5, 2, [2, 3]), (6, 3, [1, 2, 3]), (6, 4, [6])]:
            shards = verify.partition(n, sizes)
            for perm in itertools.permutations(range(n)):
                self.assertEqual(
                    verify.sharded_bottom_k(perm, k, shards),
                    verify.centralized_bottom_k(perm, k),
                )

    def test_merge_trees_equal_centralized_for_every_ordering(self):
        # EXACT: different merge trees through Shard/Merger == centralized.
        import itertools

        n, k = 6, 2
        sizes = [1, 2, 3]
        starts = [0, 1, 3, 6]
        for perm in itertools.permutations(range(n)):
            keys = scaled_keys(perm)
            central = Shard("c", k, ScriptedRandom(keys))
            central.offer_many(range(n))
            # Tree 1: flat merge of three shards.
            flat = Merger(k)
            # Tree 2: hierarchical ((s0+s1)+s2).
            left, root = Merger(k), Merger(k)
            for i in range(3):
                shard = Shard(f"s{i}", k, ScriptedRandom(keys[starts[i]:starts[i + 1]]))
                shard.offer_many(range(starts[i], starts[i + 1]))
                flat.merge(shard.snapshot())
                (left if i < 2 else root).merge(shard.snapshot())
            root.merge(left.snapshot("L"))
            expected = set(central.sample())
            self.assertEqual(set(flat.sample()), expected)
            self.assertEqual(set(root.sample()), expected)


class MergeSemanticsTest(unittest.TestCase):
    def test_retry_merge_is_idempotent(self):
        shard = Shard("s", k=3, rng=RecordedRandom(7))
        shard.offer_many(range(10))
        snap = shard.snapshot()
        merger = Merger(k=3)
        merger.merge(snap)
        merger.merge(snap)  # retried upload of the same state
        merger.merge(snap)
        self.assertEqual(merger.n, 10)  # not 30
        self.assertEqual(len(merger.sample()), 3)

    def test_append_after_merge_replaces_not_accumulates(self):
        rng_a, rng_b = RecordedRandom(11), RecordedRandom(11)
        shard = Shard("s", k=3, rng=rng_a)
        central = Shard("c", k=3, rng=rng_b)
        merger = Merger(k=3)
        shard.offer_many(range(4))
        merger.merge(shard.snapshot())
        self.assertEqual(merger.n, 4)
        shard.offer_many(range(4, 7))  # shard keeps growing
        merger.merge(shard.snapshot())  # newer state replaces the old one
        central.offer_many(range(7))
        self.assertEqual(merger.n, 7)  # not 4 + 7
        self.assertEqual(set(merger.sample()), set(central.sample()))

    def test_hierarchical_merge_with_uneven_shards(self):
        n, k = 8, 3
        rng = RecordedRandom(5)
        keys = [rng.random() for _ in range(n)]
        central = Shard("c", k, ScriptedRandom(keys))
        central.offer_many(range(n))
        sizes = [3, 2, 3]
        starts = [0, 3, 5, 8]
        shards = []
        for i in range(3):
            shard = Shard(f"s{i}", k, ScriptedRandom(keys[starts[i]:starts[i + 1]]))
            shard.offer_many(range(starts[i], starts[i + 1]))
            shards.append(shard)
        left, right, root = Merger(k), Merger(k), Merger(k)
        left.merge(shards[0].snapshot())
        left.merge(shards[1].snapshot())
        right.merge(shards[2].snapshot())
        root.merge(left.snapshot("left"))
        root.merge(right.snapshot("right"))
        self.assertEqual(root.n, n)
        self.assertEqual(set(root.sample()), set(central.sample()))

    def test_k_mismatch_rejected(self):
        shard = Shard("s", k=2, rng=RecordedRandom(1))
        shard.offer_many(range(5))
        with self.assertRaises(ValueError):
            Merger(k=3).merge(shard.snapshot())


class CorrectionAndRetractionTest(unittest.TestCase):
    def test_length_correction_via_replacement_snapshot(self):
        # A shard overcounted / retracted items 3..4: the origin rebuilds a
        # consistent state (same keys for the surviving items) and re-sends.
        shard = Shard("s", k=2, rng=RecordedRandom(3))
        shard.offer_many(range(5))
        merger = Merger(k=2)
        merger.merge(shard.snapshot())
        self.assertEqual(merger.n, 5)
        corrected = Shard("s", k=2, rng=RecordedRandom(3))
        corrected.offer_many(range(3))  # items 3, 4 retracted at the origin
        merger.merge(corrected.snapshot())
        self.assertEqual(merger.n, 3)
        self.assertTrue(set(merger.sample()) <= {0, 1, 2})

    def test_correction_propagates_up_the_tree(self):
        shard = Shard("s", k=2, rng=RecordedRandom(4))
        shard.offer_many(range(6))
        mid, root = Merger(k=2), Merger(k=2)
        mid.merge(shard.snapshot())
        root.merge(mid.snapshot("mid"))
        self.assertEqual(root.n, 6)
        corrected = Shard("s", k=2, rng=RecordedRandom(4))
        corrected.offer_many(range(4))
        mid.merge(corrected.snapshot())
        root.merge(mid.snapshot("mid"))  # re-send the corrected subtree
        self.assertEqual(root.n, 4)
        self.assertTrue(set(root.sample()) <= {0, 1, 2, 3})

    def test_inconsistent_correction_rejected(self):
        # Boundary: a bare length edit without matching entries is invalid.
        entries = (Entry("s", 0, 0.1, "a"), Entry("s", 1, 0.2, "b"))
        with self.assertRaises(ValueError):  # n lowered, entries not rebuilt
            ShardSnapshot("s", k=2, n=1, entries=entries)
        with self.assertRaises(ValueError):  # seq outside [0, n)
            ShardSnapshot("s", k=2, n=2,
                          entries=(Entry("s", 0, 0.1, "a"), Entry("s", 5, 0.2, "b")))
        with self.assertRaises(ValueError):  # duplicate identity
            ShardSnapshot("s", k=2, n=2,
                          entries=(Entry("s", 0, 0.1, "a"), Entry("s", 0, 0.2, "b")))
        with self.assertRaises(ValueError):  # key outside [0, 1)
            ShardSnapshot("s", k=2, n=2,
                          entries=(Entry("s", 0, 1.5, "a"), Entry("s", 1, 0.2, "b")))


class RandomSourceTest(unittest.TestCase):
    def test_seed_and_draws_recorded_and_reproducible(self):
        rng1, rng2 = RecordedRandom(42), RecordedRandom(42)
        s1 = Shard("s", k=4, rng=rng1)
        s2 = Shard("s", k=4, rng=rng2)
        s1.offer_many(range(50))
        s2.offer_many(range(50))
        self.assertEqual(rng1.seed, 42)
        self.assertEqual(rng1.draws, 50)  # one draw per offered item
        self.assertEqual(s1.sample(), s2.sample())  # same seed -> same sample


class EmpiricalTest(unittest.TestCase):
    """EMPIRICAL estimates (seeded Monte-Carlo), not exact proofs."""

    def test_empirical_uniformity(self):
        # EMPIRICAL: 20 subsets, expected 1000 each at 20000 trials.
        counts = verify.empirical_subset_counts(n=6, k=3, trials=20000, seed=123)
        self.assertEqual(len(counts), 20)
        for c in counts.values():
            self.assertLess(abs(c - 1000), 150)  # ~4.7 sigma

    def test_naive_concatenation_is_biased(self):
        # EMPIRICAL contrast: pooling shard samples then picking k uniformly
        # over-represents small shards; bottom-k does not.
        rng = RecordedRandom(99)
        small_hits, large_hits = 0, 0
        trials = 4000
        for _ in range(trials):
            pool = []
            for size in (10, 990):  # naive per-shard reservoir, no keys
                reservoir = []
                for i in range(size):
                    item = (size, i)
                    if len(reservoir) < 5:
                        reservoir.append(item)
                    else:
                        j = int(rng.random() * (i + 1))
                        if j < 5:
                            reservoir[j] = item
                pool.extend(reservoir)
            picked = {pool[int(rng.random() * len(pool))] for _ in range(5)}
            if (10, 0) in picked:
                small_hits += 1
            if (990, 500) in picked:
                large_hits += 1
        # Small-shard item expected ~1000 hits, large-shard item ~10.
        self.assertGreater(small_hits, 10 * max(large_hits, 1))


if __name__ == "__main__":
    unittest.main()
