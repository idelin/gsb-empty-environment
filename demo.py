"""Runnable demo for reservoir_sampling.  Run: python3.11 demo.py

Sections 1-2 use a seeded PRNG (reproducible). Section 3 prints an EXACT
exhaustive enumeration; section 4 prints an EMPIRICAL estimate.
"""

import itertools
import math
import random

from reservoir_sampling import ReservoirSample


def rng_from_keys(keys):
    it = iter(keys)
    return lambda: next(it)


def main():
    print("=== 1. Sharded stream, hierarchical merge vs central (seeded) ===")
    stream = [f"event-{i}" for i in range(1000)]
    seed = 20260927
    keys = [random.Random(seed + i).random() for i in range(len(stream))]

    central = ReservoirSample(5, shard_id="central", rng=rng_from_keys(iter(keys)))
    central.add_all(stream)

    sizes = [100, 250, 50, 600]  # deliberately unequal shard sizes
    shards, pos = [], 0
    for i, size in enumerate(sizes):
        s = ReservoirSample(
            5, shard_id=f"shard-{i}", rng=rng_from_keys(iter(keys[pos:pos + size]))
        )
        s.add_all(stream[pos:pos + size])
        shards.append(s)
        pos += size

    left = ReservoirSample.merged(shards[:2], shard_id="node-L")
    right = ReservoirSample.merged(shards[2:], shard_id="node-R")
    root = ReservoirSample.merged([left, right], shard_id="root")

    print("central  :", central.sample())
    print("merged   :", root.sample())
    print("identical:", central.sample() == root.sample())
    print("metadata :", root.metadata())

    print()
    print("=== 2. Retry / duplicate merge is idempotent ===")
    before = (root.keyed_sample(), root.n)
    root.merge(left).merge(right).merge(shards[0])
    after = (root.keyed_sample(), root.n)
    print("sample and n unchanged after duplicate merges:", before == after)
    print("n =", root.n, "(still 1000, not 1000 + re-merged lengths)")

    print()
    print("=== 3. EXACT: all key assignments, n=4, k=2 (24 permutations) ===")
    n, k = 4, 2
    base = [(i + 1) / (n + 1) for i in range(n)]
    counts = {}
    for perm in itertools.permutations(base):
        s = ReservoirSample(k, shard_id="x", rng=rng_from_keys(iter(perm)))
        s.add_all(range(n))
        subset = tuple(sorted(s.sample()))
        counts[subset] = counts.get(subset, 0) + 1
    for subset in sorted(counts):
        print(f"  subset {subset}: {counts[subset]} of 24")
    print("  -> every k-subset equally likely: EXACT (enumerated, not estimated)")

    print()
    print("=== 4. EMPIRICAL: seeded PRNG frequencies, n=6, k=3, 60000 trials ===")
    trials = 60_000
    counts = {}
    for t in range(trials):
        s = ReservoirSample(3, shard_id="s", seed=t)
        s.add_all(range(6))
        subset = tuple(sorted(s.sample()))
        counts[subset] = counts.get(subset, 0) + 1
    expected = trials / math.comb(6, 3)
    lo, hi = min(counts.values()), max(counts.values())
    print(f"  20 subsets, expected {expected:.0f} each; observed min={lo}, max={hi}")
    print("  -> consistent with uniformity: EMPIRICAL estimate, not a proof")


if __name__ == "__main__":
    main()
