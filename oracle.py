"""独立的全量回放 oracle。

与 token_bucket.py 不共享任何实现：每次调用都从零开始完整重放，
用于对拍验证增量修订引擎。输入输出都是纯数据（tuple / Fraction），
不依赖引擎的内部类型。

事件：``(time, seq)`` 列表，可乱序传入，oracle 自行排序。
配置：``(start, capacity, rate)`` 三元组列表。
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import List, Sequence, Tuple


@dataclass(frozen=True)
class OracleDecision:
    time: Fraction
    seq: int
    level_before: Fraction
    allowed: bool
    level_after: Fraction


def _as_fraction(x) -> Fraction:
    return x if isinstance(x, Fraction) else Fraction(x)


def _segment_at(segments, t):
    chosen = segments[0]
    for seg in segments:
        if seg[0] <= t:
            chosen = seg
        else:
            break
    return chosen


def _refill(segments, level, t_from, t_to):
    """逐段积分补充量，段边界与 t_to 处都按生效容量截断。"""
    cuts = sorted({s[0] for s in segments if t_from < s[0] < t_to})
    points = [t_from] + cuts + [t_to]
    for a, b in zip(points, points[1:]):
        _, cap, rate = _segment_at(segments, a)
        level = min(cap, level)
        level = min(cap, level + rate * (b - a))
    _, cap_at_end, _ = _segment_at(segments, t_to)
    return min(level, cap_at_end)


def full_replay(
    events: Sequence[Tuple[object, int]],
    segments: Sequence[Tuple[object, object, object]],
) -> List[OracleDecision]:
    """从零完整重放，返回每个事件的判定（按 (time, seq) 序）。"""
    segs = sorted(
        (_as_fraction(s), _as_fraction(c), _as_fraction(r)) for s, c, r in segments
    )
    if not segs:
        raise ValueError("empty config")
    ordered = sorted((_as_fraction(t), int(q)) for t, q in events)

    out: List[OracleDecision] = []
    level = None
    last_t = None
    for t, q in ordered:
        if level is None:
            level = _segment_at(segs, t)[1]
        else:
            level = _refill(segs, level, last_t, t)
        allowed = level >= 1
        after = level - 1 if allowed else level
        out.append(OracleDecision(t, q, level, allowed, after))
        level, last_t = after, t
    return out
