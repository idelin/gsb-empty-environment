"""Verification helpers.

EXACT results are derived by enumerating all n! distinct-key orderings:
bottom-k depends only on the relative order of the keys, and every
ordering is equally likely, so this enumerates the exact output
distribution.  EMPIRICAL results are Monte-Carlo estimates from a seeded
RNG and are always labelled as such.
"""

from __future__ import annotations

from collections import Counter
from itertools import permutations
from math import comb, factorial
from typing import FrozenSet, List, Optional, Sequence, Tuple

from .rng import RecordedRandom

__all__ = [
    "partition",
    "centralized_bottom_k",
    "sharded_bottom_k",
    "exact_subset_counts",
    "expected_count_per_subset",
    "check_exact_uniformity",
    "empirical_subset_counts",
]


def partition(n: int, sizes: Sequence[int]) -> List[List[int]]:
    """Split range(n) into consecutive shards of the given sizes."""
    if sum(sizes) != n:
        raise ValueError("sizes must sum to n")
    shards, start = [], 0
    for size in sizes:
        shards.append(list(range(start, start + size)))
        start += size
    return shards


def centralized_bottom_k(keys: Sequence[float], k: int) -> FrozenSet[int]:
    order = sorted(range(len(keys)), key=lambda i: (keys[i], i))
    return frozenset(order[:k])


def sharded_bottom_k(
    keys: Sequence[float], k: int, shards: Sequence[Sequence[int]]
) -> FrozenSet[int]:
    """One-level shard merge; equal to any merge tree and to centralized."""
    pool: List[int] = []
    for shard in shards:
        order = sorted(shard, key=lambda i: (keys[i], i))
        pool.extend(order[:k])
    pool.sort(key=lambda i: (keys[i], i))
    return frozenset(pool[:k])


def exact_subset_counts(
    n: int, k: int, shards: Optional[Sequence[Sequence[int]]] = None
) -> Counter:
    """EXACT: count output subsets over all n! equally likely key orderings."""
    counts: Counter = Counter()
    for perm in permutations(range(n)):
        if shards is None:
            result = centralized_bottom_k(perm, k)
        else:
            result = sharded_bottom_k(perm, k, shards)
        counts[result] += 1
    return counts


def expected_count_per_subset(n: int, k: int) -> int:
    return factorial(n) // comb(n, k)


def check_exact_uniformity(
    n: int, k: int, shards: Optional[Sequence[Sequence[int]]] = None
) -> Tuple[bool, Counter]:
    """EXACT check: every k-subset must appear exactly n!/C(n,k) times."""
    counts = exact_subset_counts(n, k, shards)
    expected = expected_count_per_subset(n, k)
    ok = len(counts) == comb(n, k) and all(c == expected for c in counts.values())
    return ok, counts


def empirical_subset_counts(
    n: int,
    k: int,
    trials: int,
    seed,
    shards: Optional[Sequence[Sequence[int]]] = None,
) -> Counter:
    """EMPIRICAL estimate from ``trials`` seeded runs."""
    rng = RecordedRandom(seed)
    counts: Counter = Counter()
    for _ in range(trials):
        keys = [rng.random() for _ in range(n)]
        if shards is None:
            result = centralized_bottom_k(keys, k)
        else:
            result = sharded_bottom_k(keys, k, shards)
        counts[result] += 1
    return counts
