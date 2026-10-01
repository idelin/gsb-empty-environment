"""按排名定位（部分）排列：factoradic / Lehmer 编码，支持大整数索引。

排名 rank ∈ [0, P(n,k)) 与 k-部分排列一一对应（精确双射）：
第 t 位的权重为 P(n-1-t, k-1-t)，数码取值范围 [0, n-t)。
借助 _VirtualSet，n 可以是天文数字而不必物化候选列表。
"""
from __future__ import annotations

import math
from bisect import bisect_left, bisect_right, insort


class _VirtualSet:
    """{0..n-1} 的虚拟有序集合，支持 O(log n) 的选择/删除，n 可为巨大整数。"""

    def __init__(self, n: int):
        self.n = n
        self.deleted: list[int] = []  # 已删除下标，保持有序

    def select(self, idx: int):
        """删除并返回第 idx 小（0 基）的可用元素。"""
        if not 0 <= idx < self.n - len(self.deleted):
            raise IndexError("select index out of range")
        lo, hi = 0, self.n - 1
        target = idx + 1
        while lo < hi:
            mid = (lo + hi) // 2
            available = mid + 1 - bisect_right(self.deleted, mid)
            if available >= target:
                hi = mid
            else:
                lo = mid + 1
        insort(self.deleted, lo)
        return lo

    def index_of(self, value: int):
        """删除 value 并返回其在可用元素中的名次（0 基）。"""
        idx = value - bisect_left(self.deleted, value)
        insort(self.deleted, value)
        return idx


def num_partial_permutations(n: int, k: int) -> int:
    """k-部分排列总数 P(n,k) = n!/(n-k)!。"""
    return math.perm(n, k)


def unrank_partial(n: int, k: int, rank: int) -> list:
    """将 rank ∈ [0, P(n,k)) 定位为一个 k-部分排列（元素取自 [0, n)）。"""
    if not 0 <= k <= n:
        raise ValueError("require 0 <= k <= n")
    total = math.perm(n, k)
    if not 0 <= rank < total:
        raise ValueError(f"rank must be in [0, {total})")
    available = _VirtualSet(n)
    out = []
    r = rank
    for t in range(k):
        w = math.perm(n - 1 - t, k - 1 - t)
        idx, r = divmod(r, w)
        out.append(available.select(idx))
    return out


def rank_partial(n: int, partial) -> int:
    """部分排列 -> 排名，unrank_partial 的逆映射。"""
    partial = list(partial)
    k = len(partial)
    if not 0 <= k <= n:
        raise ValueError("require 0 <= len(partial) <= n")
    available = _VirtualSet(n)
    rank = 0
    for t, v in enumerate(partial):
        if not 0 <= v < n:
            raise ValueError("element out of range")
        w = math.perm(n - 1 - t, k - 1 - t)
        rank += available.index_of(v) * w
    return rank


def unrank_permutation(n: int, rank: int) -> list:
    """rank ∈ [0, n!) -> 全排列。"""
    return unrank_partial(n, n, rank)


def rank_permutation(perm) -> int:
    """全排列 -> 排名。"""
    perm = list(perm)
    return rank_partial(len(perm), perm)
