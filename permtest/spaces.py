"""Permutation spaces defined over observation identities.

Every design defines its legal permutation group over *observation
indices*, never over distinct values.  Duplicate (tied) values therefore
keep their full probability multiplicity: two permutations that swap
identical values are still distinct elements of the group and are
counted separately.
"""
from __future__ import annotations

import math
from typing import List, Sequence, Tuple


def unrank_combination(n: int, k: int, rank: int) -> Tuple[int, ...]:
    """Return the ``rank``-th k-combination of range(n) in lexicographic order.

    The ordering matches ``itertools.combinations(range(n), k)``, so rank 0
    is always ``(0, 1, ..., k-1)`` -- the original labelling when the first
    ``k`` observations form group 1.
    """
    if k < 0 or n < 0 or k > n:
        raise ValueError("invalid (n, k)")
    if not 0 <= rank < math.comb(n, k):
        raise ValueError("rank out of range")
    combo: List[int] = []
    start = 0
    for i in range(k):
        remaining = k - i - 1
        c = start
        while True:
            count = math.comb(n - c - 1, remaining) if n - c - 1 >= remaining else 0
            if rank < count:
                combo.append(c)
                start = c + 1
                break
            rank -= count
            c += 1
    return tuple(combo)


class IndependentTwoSampleSpace:
    """All ways to assign ``n1`` of N observation indices to group 1.

    The legal permutation group has size C(N, n1); unequal group sizes are
    supported (n1 != n2 is fine).  Rank 0 is the observed assignment.
    """

    design = "independent-two-sample"

    def __init__(self, values: Sequence[float], n1: int):
        self.values = [float(v) for v in values]
        self.n = len(self.values)
        self.n1 = int(n1)
        self.n2 = self.n - self.n1
        if not 1 <= self.n1 <= self.n - 1:
            raise ValueError("n1 must satisfy 1 <= n1 <= N - 1")
        self.size = math.comb(self.n, self.n1)
        self._total = math.fsum(self.values)

    def observed_rank(self) -> int:
        return 0

    def _group1_sum(self, indices) -> float:
        return math.fsum(self.values[i] for i in indices)

    def statistic(self, rank: int) -> float:
        """Difference of group means (group1 - group2) for a given rank."""
        indices = unrank_combination(self.n, self.n1, rank)
        s1 = self._group1_sum(indices)
        s2 = self._total - s1
        return s1 / self.n1 - s2 / self.n2

    def sample_statistic(self, rng) -> float:
        indices = rng.sample(range(self.n), self.n1)
        s1 = self._group1_sum(indices)
        s2 = self._total - s1
        return s1 / self.n1 - s2 / self.n2


class PairedSwapSpace:
    """Sign-flip (paired) permutation space over n paired differences.

    The legal group has 2**n elements: each pair may be swapped or not.
    Rank 0 (all signs positive) is the observed arrangement.
    """

    design = "paired-swap"

    def __init__(self, differences: Sequence[float]):
        self.differences = [float(d) for d in differences]
        self.n = len(self.differences)
        if self.n < 1:
            raise ValueError("need at least one pair")
        self.size = 1 << self.n

    def observed_rank(self) -> int:
        return 0

    def statistic(self, rank: int) -> float:
        total = 0.0
        for i, d in enumerate(self.differences):
            total += d if (rank >> i) & 1 == 0 else -d
        return total / self.n

    def sample_statistic(self, rng) -> float:
        return self.statistic(rng.getrandbits(self.n))


class StratifiedSpace:
    """Product of per-stratum independent two-sample spaces.

    Labels are permuted *within* each stratum only; the legal group is the
    direct product of the stratum-level groups, with size
    ``prod C(N_s, n1_s)``.  The statistic is the pooled difference of
    means across strata.  Rank 0 is the observed arrangement.
    """

    design = "stratified"

    def __init__(self, strata: Sequence[Tuple[Sequence[float], int]]):
        if not strata:
            raise ValueError("need at least one stratum")
        self.strata = [IndependentTwoSampleSpace(values, n1) for values, n1 in strata]
        self._sizes = [s.size for s in self.strata]
        size = 1
        for s in self._sizes:
            size *= s
        self.size = size
        self._n1 = sum(s.n1 for s in self.strata)
        self._n2 = sum(s.n2 for s in self.strata)

    def observed_rank(self) -> int:
        return 0

    def _decompose(self, rank: int) -> List[int]:
        ranks = []
        for s in reversed(self._sizes):
            ranks.append(rank % s)
            rank //= s
        return list(reversed(ranks))

    def statistic(self, rank: int) -> float:
        s1 = 0.0
        s2 = 0.0
        for space, r in zip(self.strata, self._decompose(rank)):
            indices = unrank_combination(space.n, space.n1, r)
            g1 = space._group1_sum(indices)
            s1 += g1
            s2 += space._total - g1
        return s1 / self._n1 - s2 / self._n2

    def sample_statistic(self, rng) -> float:
        s1 = 0.0
        s2 = 0.0
        for space in self.strata:
            indices = rng.sample(range(space.n), space.n1)
            g1 = space._group1_sum(indices)
            s1 += g1
            s2 += space._total - g1
        return s1 / self._n1 - s2 / self._n2
