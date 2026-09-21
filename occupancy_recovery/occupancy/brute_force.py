"""小数据逐端点扫描的参考实现，仅用于测试与教学核验。

每次查询都收集当前全部有效区间在查询范围内的变化端点，逐段（半开）扫描
并直接数出每段的活跃占用；相邻分段只有在"占用集合完全相同"时才合并。
零占用也是合法结果：若峰值为 0，则把整个查询范围作为一个 0 峰值时段。
"""

from __future__ import annotations

from typing import List, Optional, Set, Tuple

from .tracker import PeakPeriod, PeakResult


def _active_at(
    intervals: List[Tuple[int, int, str]], probe: int
) -> Tuple[str, ...]:
    return tuple(sorted(oid for s, e, oid in intervals if s <= probe < e))


def _merge_periods(raw: List[PeakPeriod]) -> List[PeakPeriod]:
    merged: List[PeakPeriod] = []
    for period in raw:
        if (
            merged
            and merged[-1].end == period.start
            and merged[-1].level == period.level
            and merged[-1].evidence == period.evidence
        ):
            merged.append(
                PeakPeriod(
                    merged.pop().start,
                    period.end,
                    period.level,
                    period.evidence,
                )
            )
        else:
            merged.append(period)
    return merged


def brute_force_peak(
    intervals: List[Tuple[int, int, str]],
    lo: Optional[int] = None,
    hi: Optional[int] = None,
) -> PeakResult:
    if lo is not None and hi is not None and hi <= lo:
        raise ValueError("query range [lo, hi) requires hi > lo")

    cuts: Set[int] = set()
    if lo is not None:
        cuts.add(lo)
    if hi is not None:
        cuts.add(hi)
    for start, end, _oid in intervals:
        touches = (lo is None or end > lo) and (hi is None or start < hi)
        if touches:
            if lo is None or start >= lo:
                cuts.add(start)
            if (hi is None or end < hi) and (lo is None or end > lo):
                cuts.add(end)
    ordered = sorted(cuts)

    raw: List[PeakPeriod] = []

    if lo is None:
        # 无左界：(-inf, 首个端点) 是首段；没有端点时整段 (-inf, hi)。
        if not ordered:
            raw.append(PeakPeriod(None, hi, 0, ()))
        else:
            first = ordered[0]
            raw.append(PeakPeriod(None, first, 0, ()))
            for left, right in zip(ordered, ordered[1:]):
                if left < right:
                    raw.append(PeakPeriod(left, right, len(_active_at(intervals, left)),
                                        _active_at(intervals, left)))
            last = ordered[-1]
            tail_end = hi if hi is not None else None
            if tail_end is None or last < tail_end:
                tail_level = len(_active_at(intervals, last)) if hi is None else 0
                raw.append(PeakPeriod(last, tail_end, tail_level,
                                      _active_at(intervals, last) if hi is None else ()))
    else:
        left = lo
        for right in ordered:
            if right > left:
                raw.append(PeakPeriod(left, right, len(_active_at(intervals, left)),
                                      _active_at(intervals, left)))
            left = right
        tail_end = hi if hi is not None else None
        if tail_end is None or left < tail_end:
            evidence = _active_at(intervals, left) if hi is None else ()
            raw.append(PeakPeriod(left, tail_end, len(evidence), evidence))

    merged = _merge_periods(raw)
    peak = max((p.level for p in merged), default=0)
    if peak == 0 and (lo is None or hi is None):
        return PeakResult(0, (), lo, hi)
    return PeakResult(
        peak,
        tuple(p for p in merged if p.level == peak),
        lo,
        hi,
    )
