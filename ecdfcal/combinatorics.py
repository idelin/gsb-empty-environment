"""Lexicographic rank/unrank of combinations and disjoint shard ranges.

Rank-based sharding lets parallel workers enumerate disjoint, contiguous
rank intervals whose union is exactly [0, C(n, k)) -- no permutation is
duplicated or missed.
"""
from __future__ import annotations

from math import comb


def unrank_combination(rank: int, n: int, k: int) -> tuple:
    """Return the `rank`-th k-subset of range(n) in lexicographic order."""
    if k < 0 or n < 0 or k > n:
        raise ValueError("require 0 <= k <= n")
    if not 0 <= rank < comb(n, k):
        raise ValueError("rank out of range")
    result = []
    start = 0
    remaining = rank
    for i in range(k):
        j = start
        while True:
            count = comb(n - j - 1, k - i - 1)
            if remaining < count:
                result.append(j)
                start = j + 1
                break
            remaining -= count
            j += 1
    return tuple(result)


def rank_combination(indices, n: int) -> int:
    """Inverse of unrank_combination for a sorted k-subset of range(n)."""
    indices = tuple(indices)
    k = len(indices)
    if any(indices[i] >= indices[i + 1] for i in range(k - 1)):
        raise ValueError("indices must be strictly increasing")
    rank = 0
    start = 0
    for i, j in enumerate(indices):
        for t in range(start, j):
            rank += comb(n - t - 1, k - i - 1)
        start = j + 1
    return rank


def shard_ranges(total: int, n_shards: int):
    """Contiguous half-open ranges covering [0, total) exactly once."""
    if total <= 0:
        raise ValueError("total must be positive")
    if n_shards <= 0:
        raise ValueError("n_shards must be positive")
    n_shards = min(n_shards, total)
    base, remainder = divmod(total, n_shards)
    ranges = []
    low = 0
    for shard in range(n_shards):
        size = base + (1 if shard < remainder else 0)
        ranges.append((low, low + size))
        low += size
    return ranges
