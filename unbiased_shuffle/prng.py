"""显式随机源与无偏范围抽样。

随机源由调用方注入（任何提供 ``getrandbits(k)`` 的对象，例如
``random.Random``）。``CountingSource`` 包装注入源并记录 seed、
抽样次数与消耗比特数；``randbelow`` 用拒绝采样避免取模偏差，
支持任意大的整数上界。
"""
from __future__ import annotations

import random


class CountingSource:
    """包装调用方注入的随机源，记录 seed 与样本量（抽样次数/比特数）。"""

    def __init__(self, inner=None, seed=None):
        if inner is None:
            inner = random.Random(seed)
        self.inner = inner
        self.seed = seed
        self.draws = 0   # getrandbits 调用次数（含被拒绝的重抽）
        self.bits = 0    # 累计消耗的比特数

    def getrandbits(self, k: int) -> int:
        if k <= 0:
            return 0
        value = self.inner.getrandbits(k)
        self.draws += 1
        self.bits += k
        return value

    def getstate(self):
        return self.inner.getstate()

    def setstate(self, state) -> None:
        self.inner.setstate(state)

    def meta(self) -> dict:
        return {"seed": self.seed, "draws": self.draws, "bits": self.bits}


def randbelow(source, n: int) -> int:
    """从 [0, n) 无偏均匀抽样（拒绝采样，无取模偏差）。

    n 可为任意大的正整数。每轮取 k = bit_length(n-1) 个比特，
    被拒绝的值等概率重抽，因此每个 r < n 的单轮接受概率均为 2**-k，
    条件分布严格均匀（精确结论，见 tests/test_randbelow.py 的枚举证明）。
    """
    if n <= 0:
        raise ValueError("n must be a positive integer")
    k = (n - 1).bit_length()
    if k == 0:  # n == 1，无需消耗随机性
        return 0
    while True:
        r = source.getrandbits(k)
        if r < n:
            return r
