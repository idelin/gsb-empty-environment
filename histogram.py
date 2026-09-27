"""计数直方图：等价类与总体分布的共同数据结构。

仅处理合成数据。计数为非负整数；概率以 fractions.Fraction 精确表示，
空直方图（total == 0）不提供概率，调用方必须先检查 is_empty，避免除零。
"""

from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction
from typing import Dict, Hashable, Iterator


@dataclass
class Histogram:
    """value -> count 的多重计数。"""

    counts: Dict[Hashable, int] = field(default_factory=dict)
    total: int = 0

    @property
    def is_empty(self) -> bool:
        return self.total == 0

    def add(self, value: Hashable, n: int = 1) -> None:
        if n <= 0:
            raise ValueError("n must be positive")
        self.counts[value] = self.counts.get(value, 0) + n
        self.total += n

    def remove(self, value: Hashable, n: int = 1) -> None:
        """数据撤回：计数不足时报错，不允许出现负计数。"""
        if n <= 0:
            raise ValueError("n must be positive")
        have = self.counts.get(value, 0)
        if have < n:
            raise ValueError(f"cannot withdraw {n} of {value!r}: only {have} present")
        if have == n:
            del self.counts[value]
        else:
            self.counts[value] = have - n
        self.total -= n

    def probability(self, value: Hashable) -> Fraction:
        if self.total == 0:
            raise ValueError("empty histogram has no probability distribution")
        return Fraction(self.counts.get(value, 0), self.total)

    def support(self) -> Iterator[Hashable]:
        return iter(self.counts)

    def copy(self) -> "Histogram":
        return Histogram(dict(self.counts), self.total)
