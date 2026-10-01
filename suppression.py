"""最少抑制记录搜索。

语义：
- 抑制 = 删除整条记录；空类视为自然满足（记录已不在发布数据集中）。
- 各等价类相互独立，全局最少抑制数 = 各类最少抑制数之和。
- 熵判定采用区间三值判定，UNDECIDED 按不满足处理（保守）。

精确解按删除总数递增枚举各类的删除向量（仅适合小域）；
贪心解每步删除当前计数最大的值的一条记录。
"""
from __future__ import annotations

from typing import Hashable, Iterator

from intervals import Decision, decide_at_least
from metrics import Config, entropy_interval_of_counts


def is_feasible(counts: dict[Hashable, int], config: Config) -> bool:
    remaining = {v: c for v, c in counts.items() if c > 0}
    if not remaining:
        return True  # 空类：记录已全部抑制，自然满足
    if len(remaining) < config.distinct_threshold:
        return False
    interval = entropy_interval_of_counts(remaining, config.log_base)
    return decide_at_least(interval, config.entropy_threshold) is Decision.SATISFIED


def _compositions(caps, total, index=0):
    """枚举 0 <= d_i <= caps[i] 且 sum(d) == total 的删除向量。"""
    if index == len(caps) - 1:
        if 0 <= total <= caps[index]:
            yield (total,)
        return
    for d in range(min(caps[index], total) + 1):
        for rest in _compositions(caps, total - d, index + 1):
            yield (d,) + rest


def min_suppression_exact(counts, config):
    """精确最少抑制：按删除总数递增枚举，返回首个可行方案 {值: 删除数}。"""
    counts = {v: c for v, c in counts.items() if c > 0}
    values = sorted(counts, key=repr)
    caps = [counts[v] for v in values]
    n = sum(caps)
    for total in range(n + 1):
        for vec in _compositions(caps, total):
            remaining = {v: c - d for v, c, d in zip(values, caps, vec)}
            if is_feasible(remaining, config):
                return {v: d for v, d in zip(values, vec) if d}
    raise AssertionError("不可达：删除全部记录必然可行")


def min_suppression_greedy(counts, config):
    """贪心抑制：每步删除当前计数最大的值的一条记录。"""
    remaining = {v: c for v, c in counts.items() if c > 0}
    plan = {v: 0 for v in remaining}
    while not is_feasible(remaining, config):
        value = max(remaining, key=lambda v: (remaining[v], repr(v)))
        remaining[value] -= 1
        plan[value] += 1
        if remaining[value] == 0:
            del remaining[value]
    return {v: d for v, d in plan.items() if d}


def total_deletions(plan):
    return sum(plan.values())
