"""Rank-sharded combination enumeration.

Combinations of ``range(n)`` taken ``k`` at a time are ordered
lexicographically (the same order as ``itertools.combinations``). Each
combination has a unique rank in ``[0, C(n, k))``. Shards are contiguous
rank ranges, so enumerating every shard covers each combination exactly
once: no duplicates, no omissions.
"""
from __future__ import annotations

from math import comb


def shard_ranges(total: int, num_shards: int) -> list[tuple[int, int]]:
    """Split ``range(total)`` into ``num_shards`` contiguous [start, stop) ranges.

    The returned ranges are disjoint and their union is exactly
    ``range(total)``; sizes differ by at most one.
    """
    if total < 0:
        raise ValueError("total must be non-negative")
    if num_shards < 1:
        raise ValueError("num_shards must be at least 1")
    base, remainder = divmod(total, num_shards)
    ranges = []
    start = 0
    for index in range(num_shards):
        size = base + (1 if index < remainder else 0)
        ranges.append((start, start + size))
        start += size
    return ranges


def unrank_combination(n: int, k: int, rank: int) -> list[int]:
    """Return the combination with the given lexicographic rank."""
    if not 0 <= k <= n:
        raise ValueError("require 0 <= k <= n")
    if not 0 <= rank < comb(n, k):
        raise ValueError("rank out of range")
    combo = []
    remaining = rank
    low = 0
    for i in range(k):
        value = low
        while True:
            count = comb(n - value - 1, k - i - 1)
            if count > remaining:
                break
            remaining -= count
            value += 1
        combo.append(value)
        low = value + 1
    return combo


def _next_combination(combo: list[int], n: int) -> list[int] | None:
    k = len(combo)
    for i in range(k - 1, -1, -1):
        if combo[i] < n - k + i:
            combo[i] += 1
            for j in range(i + 1, k):
                combo[j] = combo[j - 1] + 1
            return combo
    return None


def iter_combination_shard(n: int, k: int, start: int, stop: int):
    """Yield combinations whose lexicographic rank lies in [start, stop).

    Concatenating the output over the ranges returned by
    ``shard_ranges(comb(n, k), s)`` reproduces
    ``itertools.combinations(range(n), k)`` exactly.
    """
    if not 0 <= k <= n:
        raise ValueError("require 0 <= k <= n")
    total = comb(n, k)
    if not 0 <= start <= stop <= total:
        raise ValueError("require 0 <= start <= stop <= comb(n, k)")
    if start == stop:
        return
    if k == 0:
        yield ()
        return
    combo = unrank_combination(n, k, start)
    for _ in range(stop - start):
        yield tuple(combo)
        combo = _next_combination(combo, n)
