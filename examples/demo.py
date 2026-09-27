"""Runnable demo:  python3.11 examples/demo.py   (from the repo root)

Sections are labelled EXACT (proved by enumerating every equally likely
key ordering) or EMPIRICAL (seeded Monte-Carlo estimate).
"""

import os
import sys
from math import comb

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from mergeable_sampler import Merger, RecordedRandom, Shard
from mergeable_sampler import verify


def exact_small_stream():
    print("== EXACT: every k-subset is equally likely (full enumeration) ==")
    n, k = 6, 3
    ok_c, counts_c = verify.check_exact_uniformity(n, k)
    shards = verify.partition(n, [1, 2, 3])
    ok_s, counts_s = verify.check_exact_uniformity(n, k, shards)
    expected = verify.expected_count_per_subset(n, k)
    print(f"stream n={n}, k={k}: enumerated {__import__('math').factorial(n)} "
          f"equally likely key orderings")
    print(f"centralized : {len(counts_c)} subsets x {expected} each -> "
          f"uniform: {ok_c}")
    print(f"shards 1/2/3: {len(counts_s)} subsets x {expected} each -> "
          f"uniform: {ok_s}, identical to centralized: {counts_c == counts_s}")
    print()


def exact_merge_trees():
    print("== EXACT: merge-tree shape does not change the result ==")
    import itertools
    from mergeable_sampler import ScriptedRandom

    n, k, sizes = 6, 2, [1, 2, 3]
    starts = [0, 1, 3, 6]
    mismatches = 0
    for perm in itertools.permutations(range(n)):
        keys = tuple((p + 0.5) / n for p in perm)
        central = Shard("c", k, ScriptedRandom(keys))
        central.offer_many(range(n))
        flat, left, root = Merger(k), Merger(k), Merger(k)
        for i in range(3):
            shard = Shard(f"s{i}", k, ScriptedRandom(keys[starts[i]:starts[i + 1]]))
            shard.offer_many(range(starts[i], starts[i + 1]))
            flat.merge(shard.snapshot())
            (left if i < 2 else root).merge(shard.snapshot())
        root.merge(left.snapshot("L"))
        expected = set(central.sample())
        if set(flat.sample()) != expected or set(root.sample()) != expected:
            mismatches += 1
    print(f"checked flat and hierarchical ((s0+s1)+s2) trees over all "
          f"720 orderings: mismatches vs centralized = {mismatches}")
    print()


def empirical_large_stream():
    print("== EMPIRICAL: seeded run over an uneven 3-shard stream ==")
    seed, k = 20260927, 10
    sizes = [1_000, 9_000, 40_000]
    rngs = [RecordedRandom(f"{seed}-shard-{i}") for i in range(len(sizes))]
    shards = [Shard(f"shard-{i}", k, rngs[i]) for i in range(len(sizes))]
    base = 0
    for shard, size in zip(shards, sizes):
        shard.offer_many(range(base, base + size))  # items are global ids
        base += size
    left, root = Merger(k), Merger(k)
    left.merge(shards[0].snapshot())
    left.merge(shards[1].snapshot())
    root.merge(left.snapshot("left-half"))
    root.merge(shards[2].snapshot())
    print(f"seed={seed}, k={k}, shard sizes={sizes}, merged n={root.n}")
    print(f"rng draws per shard: {[r.draws for r in rngs]}")
    print(f"sample (global item ids): {sorted(root.sample())}")
    print()


def empirical_uniformity():
    print("== EMPIRICAL: subset frequencies, seeded Monte-Carlo ==")
    n, k, trials = 6, 3, 20000
    counts = verify.empirical_subset_counts(n, k, trials, seed=123)
    expected = trials / comb(n, k)
    lo, hi = min(counts.values()), max(counts.values())
    print(f"n={n}, k={k}, trials={trials}, seed=123: {len(counts)} subsets, "
          f"expected ~{expected:.0f} each, observed min={lo} max={hi} "
          f"(estimate, not a proof)")
    print()


def semantics():
    print("== Merge semantics: retry, append, correction, k=0, short stream ==")
    shard = Shard("s", k=3, rng=RecordedRandom(7))
    shard.offer_many(range(10))
    merger = Merger(k=3)
    merger.merge(shard.snapshot())
    merger.merge(shard.snapshot())  # retried upload
    print(f"retry-safe: merged n={merger.n} (not 20)")
    shard.offer_many(range(10, 15))
    merger.merge(shard.snapshot())
    print(f"append-friendly: merged n={merger.n} (not 25)")
    corrected = Shard("s", k=3, rng=RecordedRandom(7))
    corrected.offer_many(range(12))  # origin retracted items 12..14
    merger.merge(corrected.snapshot())
    print(f"length correction by replacement: merged n={merger.n}")
    empty = Shard("z", k=0, rng=RecordedRandom(1))
    empty.offer_many(range(1000))
    print(f"k=0: n={empty.n}, retained entries={len(empty.snapshot().entries)}")
    short = Shard("t", k=5, rng=RecordedRandom(2))
    short.offer_many(["a", "b"])
    print(f"stream shorter than k: n={short.n}, sample={short.sample()}")


if __name__ == "__main__":
    exact_small_stream()
    exact_merge_trees()
    empirical_large_stream()
    empirical_uniformity()
    semantics()
