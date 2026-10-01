"""Rank / unrank partial permutations (k-permutations of n elements).

The P(n, k) = n! / (n-k)! ordered k-permutations of {0, ..., n-1} are put
in lexicographic order; ``unrank_partial`` maps a rank r in [0, P(n, k)) to
its permutation and ``rank_partial`` is the exact inverse.  Ranks and counts
are plain Python integers, so arbitrarily large n and k (big-integer ranks)
work unchanged.
"""

from __future__ import annotations


def num_partial_permutations(n: int, k: int) -> int:
    """Return P(n, k) = n * (n-1) * ... * (n-k+1)."""
    if not (isinstance(n, int) and isinstance(k, int)) or not (0 <= k <= n):
        raise ValueError("require integers with 0 <= k <= n")
    result = 1
    for t in range(k):
        result *= n - t
    return result


def unrank_partial(n: int, k: int, rank: int) -> list:
    """Return the k-permutation at position ``rank`` in lexicographic order."""
    total = num_partial_permutations(n, k)
    if not isinstance(rank, int) or not 0 <= rank < total:
        raise ValueError(f"rank must be in [0, {total})")
    remaining = list(range(n))
    out = []
    r = rank
    for t in range(k):
        block = num_partial_permutations(n - t - 1, k - t - 1)
        digit, r = divmod(r, block)
        out.append(remaining.pop(digit))
    return out


def rank_partial(n: int, k: int, perm) -> int:
    """Inverse of :func:`unrank_partial`; validates the permutation."""
    perm = list(perm)
    if len(perm) != k or not (0 <= k <= n):
        raise ValueError("perm must have length k with 0 <= k <= n")
    if len(set(perm)) != k or any(not isinstance(x, int) or not 0 <= x < n for x in perm):
        raise ValueError("perm must contain k distinct values from [0, n)")
    remaining = list(range(n))
    rank = 0
    for t, x in enumerate(perm):
        digit = remaining.index(x)
        remaining.pop(digit)
        rank += digit * num_partial_permutations(n - t - 1, k - t - 1)
    return rank
